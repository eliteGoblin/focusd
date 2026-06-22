package scheduler

import (
	"errors"
	"testing"

	"github.com/eliteGoblin/focusd/platform/internal/core/state"
)

// TestRegisterIntegritySweep_AddsEntry: the synthetic @every 1m sweep
// registers without error.
func TestRegisterIntegritySweep_AddsEntry(t *testing.T) {
	s, _ := newSched(t)
	before := len(s.cron.Entries())
	if err := s.RegisterIntegritySweep(func() error { return nil }); err != nil {
		t.Fatalf("RegisterIntegritySweep: %v", err)
	}
	if got := len(s.cron.Entries()); got != before+1 {
		t.Fatalf("expected one new cron entry, got %d (was %d)", got, before)
	}
}

// TestRegisterIntegritySweep_RecordsFailureEvent: when the sweep func
// errors, the wrapped entry records an integrity_sweep_failed event so a
// wedged sweep can't hide behind a green status.
func TestRegisterIntegritySweep_RecordsFailureEvent(t *testing.T) {
	s, db := newSched(t)
	// Register a sweep that always fails, then invoke the registered entry
	// directly (we don't want to wait a real minute).
	if err := s.RegisterIntegritySweep(func() error { return errors.New("boom") }); err != nil {
		t.Fatalf("RegisterIntegritySweep: %v", err)
	}
	entries := s.cron.Entries()
	if len(entries) == 0 {
		t.Fatal("no cron entry registered")
	}
	entries[len(entries)-1].Job.Run() // trigger the wrapped func

	ev, err := db.Events.Recent(10)
	if err != nil {
		t.Fatalf("recent events: %v", err)
	}
	found := false
	for _, e := range ev {
		if e.EventType == state.EventIntegritySweepFailed && e.Severity == state.SeverityError {
			found = true
		}
	}
	if !found {
		t.Error("expected plugin_integrity_sweep_failed (error) event")
	}
}
