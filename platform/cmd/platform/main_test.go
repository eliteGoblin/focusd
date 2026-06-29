package main

import (
	"errors"
	"testing"

	"github.com/eliteGoblin/focusd/platform/internal/core/state"
)

// TestHistoryWithRetry_SeparatesErrorFromEmpty is the bug-#2 guard: the status
// reader must NOT conflate a transient read ERROR with a genuine empty history.
func TestHistoryWithRetry_SeparatesErrorFromEmpty(t *testing.T) {
	seeded := []state.JobRun{{JobID: "kill-steam-kill", Status: "ok"}}

	t.Run("retries past a transient error then succeeds", func(t *testing.T) {
		calls := 0
		query := func(string, int) ([]state.JobRun, error) {
			calls++
			if calls < historyReadAttempts {
				return nil, errors.New("database is locked (SQLITE_BUSY)")
			}
			return seeded, nil
		}
		runs, err := historyWithRetry(query, "kill-steam-kill")
		if err != nil {
			t.Fatalf("err = %v, want nil after retry recovered", err)
		}
		if len(runs) != 1 {
			t.Fatalf("runs = %d, want 1 — a transient error must not surface as missing history", len(runs))
		}
		if calls != historyReadAttempts {
			t.Fatalf("calls = %d, want %d (should retry until success)", calls, historyReadAttempts)
		}
	})

	t.Run("a genuine empty result is returned immediately, not retried", func(t *testing.T) {
		calls := 0
		query := func(string, int) ([]state.JobRun, error) {
			calls++
			return nil, nil // no error, no rows: a real "never ran"
		}
		runs, err := historyWithRetry(query, "kill-steam-kill")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if len(runs) != 0 {
			t.Fatalf("runs = %d, want 0", len(runs))
		}
		if calls != 1 {
			t.Fatalf("calls = %d, want 1 — an empty result is a real answer and must not be retried", calls)
		}
	})

	t.Run("a persistent error is returned as an error, never as empty history", func(t *testing.T) {
		wantErr := errors.New("disk I/O error")
		query := func(string, int) ([]state.JobRun, error) {
			return nil, wantErr
		}
		runs, err := historyWithRetry(query, "kill-steam-kill")
		if err == nil {
			t.Fatal("err = nil, want the read error — a persistent read error must be reported, not masked as 'no runs'")
		}
		if len(runs) != 0 {
			t.Fatalf("runs = %d, want 0 on error", len(runs))
		}
	})
}
