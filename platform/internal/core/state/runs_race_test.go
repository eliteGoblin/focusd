package state

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestHistory_NeverEmptyUnderConcurrentWrites reproduces the status-flip bug:
// a read-only connection (the `status` reader) must NEVER read back 0 rows for
// a job that has already recorded at least one run, even while a separate
// read-write connection (the scheduler) is continuously inserting runs.
//
// On origin/master (rollback-journal mode) this fails: the read-only
// connection transiently observes an empty result, which the status reader
// maps to "no runs yet" → UNKNOWN, flipping the overall verdict.
func TestHistory_NeverEmptyUnderConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	rw, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rw.Close()

	const jobID = "kill-steam-kill"
	// Seed: after this, the job has >=1 run forever.
	id, err := rw.Runs.Start(jobID, "kill-steam", "1.0.0", "test")
	if err != nil {
		t.Fatalf("seed Start: %v", err)
	}
	if err := rw.Runs.Finish(JobRun{ID: id, Status: "ok"}); err != nil {
		t.Fatalf("seed Finish: %v", err)
	}

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer ro.Close()

	deadline := time.Now().Add(2 * time.Second)
	var stop atomic.Bool
	var wg sync.WaitGroup

	// Writer: mimic the scheduler hammering Start/Finish for this job.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			rid, werr := rw.Runs.Start(jobID, "kill-steam", "1.0.0", "scheduler")
			if werr != nil {
				continue
			}
			_ = rw.Runs.Finish(JobRun{ID: rid, Status: "ok"})
		}
	}()

	// Reader: mimic `status` calling History(jobID, 1) repeatedly.
	var emptyReads, totalReads atomic.Int64
	wg.Add(1)
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			runs, rerr := ro.Runs.History(jobID, 1)
			if rerr != nil {
				t.Errorf("History error: %v", rerr)
				continue
			}
			totalReads.Add(1)
			if len(runs) == 0 {
				emptyReads.Add(1)
			}
		}
		stop.Store(true)
	}()

	wg.Wait()

	t.Logf("reads=%d empty=%d", totalReads.Load(), emptyReads.Load())
	if n := emptyReads.Load(); n != 0 {
		t.Fatalf("read 0 rows %d times for a job with >=1 recorded run — status would flip to 'no runs yet'", n)
	}
}

// TestOpen_UsesWAL pins the root-cause fix: the writer DB must be in WAL mode
// so the read-only status snapshot never contends with the scheduler. A
// regression to rollback-journal mode reintroduces the status flip.
func TestOpen_UsesWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var mode string
	if err := db.sql.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}
