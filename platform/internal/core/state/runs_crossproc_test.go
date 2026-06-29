package state

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Env handshake for the re-exec child reader. When set, TestStatusReader_child
// becomes the reader subprocess; otherwise it skips (it is driven only by the
// parent test below).
const (
	envCrossProcDB  = "FOCUSD_TEST_RO_DB"
	envCrossProcDur = "FOCUSD_TEST_RO_DUR_MS"
	crossProcJobID  = "kill-steam-kill"
)

var childResultRe = regexp.MustCompile(`CHILD_RESULT reads=(\d+) empty=(\d+) errs=(\d+) minrows=(\d+)`)

// TestStatusReader_CrossProcess_SeesCommittedRuns is the decisive reproduction
// of the live status flip. A SEPARATE OS PROCESS (re-exec of the test binary)
// opens the DB read-only — exactly like `daemon status` — and polls
// Runs.History(jobID, 1) while THIS process continuously commits runs. Once a
// run is committed, the cross-process reader must observe >=1 row within a
// bounded time and must NEVER spuriously read 0 or error.
//
// This is the gap the earlier WAL fix (#74) missed: its test used a same-uid,
// same-process read-only connection, which can attach the WAL's -shm and so
// always saw committed runs — passing in test while the live cross-process
// reader, unable to attach a hot -wal, read the stale main file and flipped the
// verdict. In rollback-journal mode the commit lands in the main file, so a
// separate process always sees it.
func TestStatusReader_CrossProcess_SeesCommittedRuns(t *testing.T) {
	if os.Getenv(envCrossProcDB) != "" {
		t.Skip("running as the re-exec child reader")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	rw, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rw.Close()

	// Seed: after this commit the job has >=1 run forever. Any subsequent
	// cross-process read of 0 rows is a currency bug.
	id, err := rw.Runs.Start(crossProcJobID, "kill-steam", "1.0.0", "test")
	if err != nil {
		t.Fatalf("seed Start: %v", err)
	}
	if err := rw.Runs.Finish(JobRun{ID: id, Status: "ok"}); err != nil {
		t.Fatalf("seed Finish: %v", err)
	}

	// Writer goroutine in THIS process: mimic the scheduler committing runs
	// while the separate reader process polls.
	const durMS = 2000
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			rid, werr := rw.Runs.Start(crossProcJobID, "kill-steam", "1.0.0", "scheduler")
			if werr != nil {
				continue
			}
			_ = rw.Runs.Finish(JobRun{ID: rid, Status: "ok"})
			time.Sleep(2 * time.Millisecond)
		}
	}()

	// Re-exec ourselves as the reader subprocess.
	cmd := exec.Command(os.Args[0], "-test.run", "^TestStatusReader_child$", "-test.v")
	cmd.Env = append(os.Environ(),
		envCrossProcDB+"="+path,
		envCrossProcDur+"="+strconv.Itoa(durMS),
	)
	out, runErr := cmd.CombinedOutput()
	stop.Store(true)
	wg.Wait()

	if runErr != nil {
		t.Fatalf("reader subprocess failed: %v\n%s", runErr, out)
	}

	m := childResultRe.FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("could not parse child result from output:\n%s", out)
	}
	reads, _ := strconv.Atoi(m[1])
	empty, _ := strconv.Atoi(m[2])
	errs, _ := strconv.Atoi(m[3])
	minrows, _ := strconv.Atoi(m[4])
	t.Logf("cross-process reader: reads=%d empty=%d errs=%d minrows=%d", reads, empty, errs, minrows)

	if reads == 0 {
		t.Fatalf("reader made no reads")
	}
	if errs != 0 {
		t.Fatalf("cross-process reader hit %d read errors — a transient read must not surface as missing history", errs)
	}
	if empty != 0 {
		t.Fatalf("cross-process reader read 0 rows %d times for a job with >=1 committed run — status would flip to 'no runs yet'", empty)
	}
	if minrows < 1 {
		t.Fatalf("cross-process reader observed minrows=%d, want >=1", minrows)
	}
}

// TestStatusReader_child is the re-exec reader subprocess. It runs only when
// envCrossProcDB is set (otherwise it skips, so a normal `go test` run is a
// no-op). It opens the DB read-only exactly as the status path does and polls
// History, reporting the worst case it observed on stdout for the parent.
func TestStatusReader_child(t *testing.T) {
	dbPath := os.Getenv(envCrossProcDB)
	if dbPath == "" {
		t.Skip("not the cross-process reader child")
	}
	durMS, _ := strconv.Atoi(os.Getenv(envCrossProcDur))
	if durMS <= 0 {
		durMS = 2000
	}

	ro, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer ro.Close()

	deadline := time.Now().Add(time.Duration(durMS) * time.Millisecond)
	var reads, empty, errs int
	minrows := 1 << 30
	for time.Now().Before(deadline) {
		runs, herr := ro.Runs.History(crossProcJobID, 1)
		reads++
		if herr != nil {
			errs++
			continue
		}
		if n := len(runs); n < minrows {
			minrows = n
		}
		if len(runs) == 0 {
			empty++
		}
	}
	if minrows == 1<<30 {
		minrows = 0
	}
	// Sentinel parsed by the parent. Use fmt (not t.Logf) so it lands on
	// stdout verbatim regardless of test log formatting.
	fmt.Printf("CHILD_RESULT reads=%d empty=%d errs=%d minrows=%d\n", reads, empty, errs, minrows)
}
