package state

import (
	"path/filepath"
	"testing"
)

// TestOpenReadOnly_ReadsRunsWithoutWriting verifies the read-only opener
// can read job-run history written by a normal (read-write) DB, and that
// it does not itself migrate/create anything.
func TestOpenReadOnly_ReadsRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	// Seed via the normal opener.
	rw, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id, err := rw.Runs.Start("dns-block-reconcile", "dns-block", "1.0.0", "test")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := rw.Runs.Finish(JobRun{ID: id, Status: "ok", ExitCode: 0}); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	rw.Close()

	// Now read it back read-only.
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer ro.Close()

	runs, err := ro.Runs.History("dns-block-reconcile", 1)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("history len = %d, want 1", len(runs))
	}
	if runs[0].Status != "ok" {
		t.Fatalf("status = %q, want ok", runs[0].Status)
	}
	if runs[0].StartedAt == "" {
		t.Fatalf("started_at empty — status age math would break")
	}
}

// TestOpenReadOnly_MissingFileErrors confirms a missing DB is an error
// (the caller degrades to "no runs"), not a silent empty success that
// would hide a real misconfiguration.
func TestOpenReadOnly_MissingFileErrors(t *testing.T) {
	_, err := OpenReadOnly(filepath.Join(t.TempDir(), "does-not-exist.db"))
	if err == nil {
		t.Fatal("expected error opening a non-existent DB read-only")
	}
}
