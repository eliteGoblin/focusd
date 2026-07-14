package scheduler

import (
	"errors"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/platform/internal/core/state"
)

// TestRegisterIntegritySweep_AddsEntry: the synthetic sweep registers one
// cron entry without error.
func TestRegisterIntegritySweep_AddsEntry(t *testing.T) {
	s, _ := newSched(t)
	before := len(s.cron.Entries())
	if err := s.RegisterIntegritySweep(2*time.Minute, func() error { return nil }); err != nil {
		t.Fatalf("RegisterIntegritySweep: %v", err)
	}
	if got := len(s.cron.Entries()); got != before+1 {
		t.Fatalf("expected one new cron entry, got %d (was %d)", got, before)
	}
}

// TestRegisterIntegritySweep_HonorsInterval: a configured interval is used as
// the sweep cadence, and a non-positive interval falls back to the 1m default
// (so a mis-set config can't disable the backstop). We assert via the cron
// entry's computed next-fire delta rather than reaching into private fields.
func TestRegisterIntegritySweep_HonorsInterval(t *testing.T) {
	cases := []struct {
		name     string
		interval time.Duration
		wantMax  time.Duration // next fire must be within this of now
	}{
		{"explicit-2m", 2 * time.Minute, 2 * time.Minute},
		{"zero-defaults-1m", 0, time.Minute},
		{"negative-defaults-1m", -5 * time.Second, time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newSched(t)
			if err := s.RegisterIntegritySweep(tc.interval, func() error { return nil }); err != nil {
				t.Fatalf("RegisterIntegritySweep: %v", err)
			}
			entries := s.cron.Entries()
			last := entries[len(entries)-1]
			// Entry.Next is only populated after cron.Start(); compute the next
			// fire directly from the parsed schedule instead.
			now := time.Now()
			until := last.Schedule.Next(now).Sub(now)
			// @every schedules next fire = now + interval; allow a small slack.
			if until <= 0 || until > tc.wantMax+time.Second {
				t.Errorf("next fire in %v, want ~<= %v (interval %v)", until, tc.wantMax, tc.interval)
			}
		})
	}
}

// TestRegisterIntegritySweep_RecordsFailureEvent: when the sweep func
// errors, the wrapped entry records an integrity_sweep_failed event so a
// wedged sweep can't hide behind a green status.
func TestRegisterIntegritySweep_RecordsFailureEvent(t *testing.T) {
	s, db := newSched(t)
	// Register a sweep that always fails, then invoke the registered entry
	// directly (we don't want to wait a real minute).
	if err := s.RegisterIntegritySweep(time.Minute, func() error { return errors.New("boom") }); err != nil {
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
