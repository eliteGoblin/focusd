// Package snapshot is the platform's status fast-path: a tiny, atomically
// written mirror of each job's LAST run (terminal status + start time),
// living next to state.db in the workdir.
//
// Why it exists (the status-flip fix). `daemon status` execs a SEPARATE
// `platform status` process that previously read run-history straight from
// the live state.db. But the running platform writes job_runs CONSTANTLY —
// every reconcile tick, for every plugin — so the read-only status query
// kept colliding with the writer's commit lock. An intermittently failed /
// timed-out read surfaced as "no runs yet" → jobs read UNKNOWN → OVERALL
// flipped HEALTHY↔UNKNOWN. Journal-mode tweaks (WAL, then rollback-journal)
// could not survive that contention because the contention WAS the problem:
// two processes fighting over one hot SQLite file.
//
// This decouples the read from the write. The reconcile loop (the only
// writer) mirrors each finished run into this file via temp-write + rename,
// so a reader never sees a partial file and never touches the contended DB.
// The status path reads THIS file — a single os.ReadFile of a few hundred
// bytes — instead of querying the live DB. No DB read in the status path →
// no lock contention → no flip.
//
// The DB remains the source of truth for full history and audit; this file
// is purely the last-run-per-job fast path. It carries ONLY non-disguised
// primitives (job ids, run statuses, timestamps) — the same data the status
// report already exposes — so there is nothing here a weak-moment self could
// use to tear protection down.
package snapshot

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileName is the snapshot's fixed basename inside the workdir (alongside
// state.db).
const FileName = "status-snapshot.json"

// fileMode is the on-disk mode for the snapshot. It is non-sensitive (the
// same job-id/status/timestamp primitives the status report already emits),
// so a status reader that lacks the writer's identity can still read it;
// the workdir's own directory mode governs who may traverse to it.
const fileMode = 0o644

// Entry is one job's last recorded run.
type Entry struct {
	Status    string    `json:"status"`
	StartedAt time.Time `json:"startedAt"`
}

// Store is the in-process writer. It owns the canonical last-run-per-job map
// in memory and rewrites the whole (tiny) file atomically on every Record,
// so a concurrent reader in another process always observes a complete,
// last-committed snapshot. Safe for concurrent use by the scheduler's job
// goroutines. A nil *Store is a no-op writer (used by in-memory/test DBs that
// have no workdir), so call sites never need to nil-check.
type Store struct {
	path    string
	mu      sync.Mutex
	entries map[string]Entry
}

// NewStore opens (or creates on first Record) the snapshot in dir. Any
// existing file is loaded best-effort so a platform restart does not blank
// the last-run history; a missing or unreadable file simply starts empty.
func NewStore(dir string) *Store {
	s := &Store{
		path:    filepath.Join(dir, FileName),
		entries: map[string]Entry{},
	}
	if m, err := read(s.path); err == nil && m != nil {
		s.entries = m
	}
	return s
}

// Record updates jobID's last-run entry and rewrites the file atomically.
// A nil receiver is a no-op (DBs without a workdir, e.g. ":memory:" tests).
func (s *Store) Record(jobID, status string, startedAt time.Time) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[jobID] = Entry{Status: status, StartedAt: startedAt.UTC()}
	return s.writeAtomic()
}

// writeAtomic serializes the whole map to a temp file in the same directory,
// then renames it over the target. os.Rename is atomic within a directory,
// so a reader sees either the old or the new file in full — never a torn
// write. Caller holds s.mu.
func (s *Store) writeAtomic() error {
	data, err := json.Marshal(s.entries)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".status-snapshot-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before the rename succeeds.
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(fileMode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

// Read returns the last-run map for the workdir's snapshot.
//
// Crucially it does NOT conflate "absent" with "errored" — repeating that
// conflation is exactly the bug this whole change exists to kill:
//   - missing file  → (nil, nil): a GENUINE fresh install with no runs yet.
//     The status path renders those jobs UNKNOWN ("warming up").
//   - read/parse err → (nil, err): a TRANSIENT failure. The caller must NOT
//     treat it as "never ran"; it degrades that single status call, not the
//     persistent verdict.
func Read(dir string) (map[string]Entry, error) {
	return read(filepath.Join(dir, FileName))
}

func read(path string) (map[string]Entry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]Entry
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}
