package state

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestHistory_NeverEmptyUnderConcurrentWrites is the same-process guard for
// the status-flip bug: a read-only connection (the `status` reader) must NEVER
// read back 0 rows for a job that already recorded at least one run, even while
// a separate read-write connection (the scheduler) inserts runs continuously.
//
// NOTE: this same-process variant is necessary but NOT sufficient — it passed
// under the earlier WAL fix too, because a same-uid in-process reader CAN
// attach the WAL's -shm and so sees committed-but-uncheckpointed runs. The live
// flip was a CROSS-PROCESS reader that could not attach a hot WAL; see
// TestStatusReader_CrossProcess_SeesCommittedRuns for the decisive reproduction.
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

// TestOpen_UsesRollbackJournal pins the root-cause fix and guards against
// re-introducing the earlier (failed) WAL attempt. The writer DB must use
// rollback-journal mode (journal_mode=delete) so every commit lands in the
// MAIN db file and is therefore visible to ANY read-only reader — including a
// separate, possibly privilege-dropped `daemon status` process — with no
// dependency on attaching a WAL -shm sidecar. A regression to WAL reintroduces
// the cross-process status flip (committed runs stranded in an unattachable
// -wal → stale 0-row reads → HEALTHY↔UNKNOWN).
func TestOpen_UsesRollbackJournal(t *testing.T) {
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
	if mode == "wal" {
		t.Fatalf("journal_mode = %q, want a rollback-journal mode (delete) — "+
			"WAL strands committed runs in the -wal and reintroduces the "+
			"cross-process status flip", mode)
	}
	if mode != "delete" {
		t.Fatalf("journal_mode = %q, want delete", mode)
	}
}
