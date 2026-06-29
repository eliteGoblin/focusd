package snapshot

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecordThenRead(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	start := time.Now().UTC().Truncate(time.Second)

	if err := s.Record("kill-steam-reconcile", "ok", start); err != nil {
		t.Fatalf("Record: %v", err)
	}

	m, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	e, ok := m["kill-steam-reconcile"]
	if !ok {
		t.Fatal("job missing from snapshot")
	}
	if e.Status != "ok" {
		t.Fatalf("status = %q, want ok", e.Status)
	}
	if !e.StartedAt.Equal(start) {
		t.Fatalf("startedAt = %v, want %v", e.StartedAt, start)
	}
}

// TestReadMissingIsNotAnError pins the no-conflation contract: an absent
// snapshot (genuine fresh install) is (nil, nil), never an error.
func TestReadMissingIsNotAnError(t *testing.T) {
	m, err := Read(t.TempDir())
	if err != nil {
		t.Fatalf("Read of absent snapshot errored: %v", err)
	}
	if m != nil {
		t.Fatalf("expected nil map for absent snapshot, got %v", m)
	}
}

// TestReadParseErrorIsAnError pins the other half of the contract: a present
// but unparseable snapshot is an ERROR, so the status path degrades the
// single call rather than asserting "no runs yet".
func TestReadParseErrorIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir); err == nil {
		t.Fatal("expected error reading a corrupt snapshot, got nil")
	}
}

// TestNewStoreLoadsExisting verifies a restart does not blank history.
func TestNewStoreLoadsExisting(t *testing.T) {
	dir := t.TempDir()
	s1 := NewStore(dir)
	if err := s1.Record("dns-block-reconcile", "ok", time.Now()); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore(dir) // simulate a process restart
	m := s2.entries
	if _, ok := m["dns-block-reconcile"]; !ok {
		t.Fatal("restarted store did not load the existing snapshot")
	}
}

// TestNilStoreRecordIsNoop ensures call sites that hold a nil *Store (e.g. an
// in-memory test DB with no workdir) can call Record unconditionally.
func TestNilStoreRecordIsNoop(t *testing.T) {
	var s *Store
	if err := s.Record("x", "ok", time.Now()); err != nil {
		t.Fatalf("nil Record: %v", err)
	}
}

// TestConcurrentWritesNeverSpuriouslyEmpty reproduces THE production symptom
// that the DB-layer fixes (orphan-sweep, WAL, rollback-journal) could not
// survive: a separate reader polling run-state while the writer records runs
// at full tilt. With the snapshot, a job that HAS run must ALWAYS read its
// last run back — never a spurious "no runs", never a torn/partial file —
// no matter how hard the writer hammers concurrently. That is the flip.
func TestConcurrentWritesNeverSpuriouslyEmpty(t *testing.T) {
	dir := t.TempDir()
	jobs := []string{
		"kill-steam-reconcile",
		"dns-block-reconcile",
		"network-block-reconcile",
		"skill-protector-reconcile",
	}

	// Seed once so every job has a known run BEFORE the reader starts: from
	// here on the reader must never see any of them missing.
	w := NewStore(dir)
	for _, j := range jobs {
		if err := w.Record(j, "ok", time.Now()); err != nil {
			t.Fatalf("seed Record: %v", err)
		}
	}

	var stop atomic.Bool
	var wg sync.WaitGroup

	// Writer: hammer the snapshot the way every-tick-every-plugin reconciles
	// hammer job_runs. This is the contention that flipped status.
	wg.Add(1)
	go func() {
		defer wg.Done()
		statuses := []string{"ok", "ok", "ok", "skipped", "failed"}
		i := 0
		for !stop.Load() {
			j := jobs[i%len(jobs)]
			st := statuses[i%len(statuses)]
			if err := w.Record(j, st, time.Now()); err != nil {
				t.Errorf("writer Record: %v", err)
				return
			}
			i++
		}
	}()

	// Readers: separate Store-less readers (mirrors the separate `platform
	// status` PROCESS) polling continuously. Any spurious "no runs" or torn
	// read is a flip and fails the test.
	const readers = 4
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 5000 && !stop.Load(); n++ {
				m, err := Read(dir)
				if err != nil {
					t.Errorf("reader Read errored (would surface as UNKNOWN flip): %v", err)
					return
				}
				if m == nil {
					t.Error("reader saw a missing snapshot after seeding (spurious 'no runs' — the flip)")
					return
				}
				for _, j := range jobs {
					e, ok := m[j]
					if !ok {
						t.Errorf("reader saw job %q missing (spurious 'no runs' — the flip)", j)
						return
					}
					if e.Status == "" {
						t.Errorf("reader saw torn/empty status for %q (partial file)", j)
						return
					}
				}
			}
		}()
	}

	time.Sleep(750 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
}
