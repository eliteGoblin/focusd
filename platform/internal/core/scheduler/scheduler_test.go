package scheduler

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/platform/internal/core/config"
	"github.com/eliteGoblin/focusd/platform/internal/core/plugin"
	"github.com/eliteGoblin/focusd/platform/internal/core/runner"
	"github.com/eliteGoblin/focusd/platform/internal/core/state"
	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
	"github.com/eliteGoblin/focusd/platform/internal/testutil"
)

func newSched(t *testing.T) (*Scheduler, *state.DB) {
	t.Helper()
	return newSchedMode(t, osadapter.ModeUser)
}

func newSchedMode(t *testing.T, mode osadapter.RunMode) (*Scheduler, *state.DB) {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(runner.New(db), db, log, mode), db
}

func dur(d time.Duration) config.Duration { return config.Duration(d) }

func TestRegisterValidJobPersists(t *testing.T) {
	s, db := newSched(t)
	p := testutil.ScriptPlugin(t, "kill-steam", `echo '{"status":"ok"}'`)
	jobs := []config.Job{{
		ID: "j1", Plugin: "kill-steam", Enabled: true,
		Schedule: "*/5 * * * *", Timeout: dur(10 * time.Second),
	}}
	n, err := s.Register(jobs, map[string]plugin.Discovered{"kill-steam": p})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if n != 1 {
		t.Fatalf("registered = %d, want 1", n)
	}
	jr, err := db.Jobs.Get("j1")
	if err != nil || jr.PluginID != "kill-steam" || jr.TimeoutMS != 10000 {
		t.Errorf("job not persisted: %+v err=%v", jr, err)
	}
	pr, _ := db.Plugins.Get("kill-steam")
	if !pr.Enabled {
		t.Error("registered plugin should be marked enabled")
	}
}

func TestRegisterSkipsDisabledAndUnavailable(t *testing.T) {
	s, db := newSched(t)
	p := testutil.ScriptPlugin(t, "ok", `echo '{"status":"ok"}'`)
	bad := plugin.Discovered{Manifest: &plugin.Manifest{ID: "rej"}, OK: false, Reason: "x"}
	jobs := []config.Job{
		{ID: "disabled", Plugin: "ok", Enabled: false, Schedule: "* * * * *"},
		{ID: "missing", Plugin: "nope", Enabled: true, Schedule: "* * * * *"},
		{ID: "rejected", Plugin: "rej", Enabled: true, Schedule: "* * * * *"},
		{ID: "good", Plugin: "ok", Enabled: true, Schedule: "* * * * *"},
	}
	n, err := s.Register(jobs, map[string]plugin.Discovered{"ok": p, "rej": bad})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("registered = %d, want 1 (only 'good')", n)
	}
	ev, _ := db.Events.Recent(10)
	if len(ev) < 2 {
		t.Errorf("expected events for missing+rejected, got %d", len(ev))
	}
}

func TestRegisterInvalidScheduleRecordsEvent(t *testing.T) {
	s, db := newSched(t)
	p := testutil.ScriptPlugin(t, "ok", `echo '{"status":"ok"}'`)
	jobs := []config.Job{{ID: "j1", Plugin: "ok", Enabled: true, Schedule: "not a cron"}}
	n, _ := s.Register(jobs, map[string]plugin.Discovered{"ok": p})
	if n != 0 {
		t.Fatalf("invalid schedule must not register, got %d", n)
	}
	ev, _ := db.Events.Recent(10)
	found := false
	for _, e := range ev {
		if e.EventType == "bad_schedule" {
			found = true
		}
	}
	if !found {
		t.Error("expected bad_schedule event")
	}
}

func TestTriggerRunsAndRecordsRun(t *testing.T) {
	s, db := newSched(t)
	p := testutil.ScriptPlugin(t, "ok", `echo '{"status":"ok","message":"done"}'`)
	j := config.Job{ID: "j1", Plugin: "ok", Enabled: true,
		Schedule: "* * * * *", Timeout: dur(5 * time.Second)}
	s.trigger(j, p)

	if s.TriggerCount("j1") != 1 {
		t.Errorf("trigger count = %d", s.TriggerCount("j1"))
	}
	last, err := db.Runs.LastByStatus("j1", state.RunStatusOK)
	if err != nil {
		t.Fatalf("run not recorded: %v", err)
	}
	if last.TriggeredBy != "scheduler" {
		t.Errorf("triggered_by = %q", last.TriggeredBy)
	}
	// Lock must be released after the run completes.
	if held, _ := db.Locks.Held("j1"); held {
		t.Error("lock not released after run")
	}
}

func TestTriggerNoOverlapSkipsWhenLocked(t *testing.T) {
	s, db := newSched(t)
	p := testutil.ScriptPlugin(t, "ok", `echo '{"status":"ok"}'`)
	j := config.Job{ID: "j1", Plugin: "ok", Enabled: true,
		Schedule: "* * * * *", AllowOverlap: false, Timeout: dur(time.Second)}

	// Simulate an in-flight run by pre-holding the lock.
	if ok, _ := db.Locks.TryAcquire("j1", 99, time.Minute); !ok {
		t.Fatal("precondition: could not pre-acquire lock")
	}
	s.trigger(j, p)

	skipped, err := db.Runs.LastByStatus("j1", state.RunStatusSkipped)
	if err != nil {
		t.Fatalf("expected a skipped run recorded: %v", err)
	}
	if skipped.Status != state.RunStatusSkipped {
		t.Errorf("status = %q", skipped.Status)
	}
	if _, err := db.Runs.LastByStatus("j1", state.RunStatusOK); err == nil {
		t.Error("job should NOT have executed while locked")
	}
}

func TestTriggerAllowOverlapIgnoresLock(t *testing.T) {
	s, db := newSched(t)
	p := testutil.ScriptPlugin(t, "ok", `echo '{"status":"ok"}'`)
	j := config.Job{ID: "j1", Plugin: "ok", Enabled: true,
		Schedule: "* * * * *", AllowOverlap: true, Timeout: dur(5 * time.Second)}

	db.Locks.TryAcquire("j1", 1, time.Minute) // held, but overlap allowed
	s.trigger(j, p)

	if _, err := db.Runs.LastByStatus("j1", state.RunStatusOK); err != nil {
		t.Errorf("allow_overlap job should run despite lock: %v", err)
	}
}

func TestCanDispatch(t *testing.T) {
	cases := []struct {
		name  string
		runAs string
		mode  osadapter.RunMode
		want  bool
	}{
		{"empty always dispatchable (user)", "", osadapter.ModeUser, true},
		{"empty always dispatchable (system)", "", osadapter.ModeSystem, true},
		{"system on system runs native", plugin.RunAsSystem, osadapter.ModeSystem, true},
		{"system on user unavailable (no escalation)", plugin.RunAsSystem, osadapter.ModeUser, false},
		{"current_user on user runs native", plugin.RunAsCurrentUser, osadapter.ModeUser, true},
		{"current_user on system runs via priv-drop", plugin.RunAsCurrentUser, osadapter.ModeSystem, true},
		{"active_user on user runs native", plugin.RunAsActiveUser, osadapter.ModeUser, true},
		{"active_user on system runs via priv-drop", plugin.RunAsActiveUser, osadapter.ModeSystem, true},
		{"unknown dispatches nowhere", "wat", osadapter.ModeUser, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanDispatch(tc.runAs, tc.mode); got != tc.want {
				t.Errorf("CanDispatch(%q,%q) = %v, want %v",
					tc.runAs, tc.mode, got, tc.want)
			}
		})
	}
}

// TestTriggerRunAsGate is the integration check at the trigger layer.
// It mirrors the table above but verifies the side effects: when the
// gate matches, the job runs and a run row is recorded; when it does
// not, no run row appears and a skip event is logged instead.
func TestTriggerRunAsGate(t *testing.T) {
	cases := []struct {
		name    string
		mode    osadapter.RunMode
		runAs   string
		wantRun bool
	}{
		{"system platform + system job runs", osadapter.ModeSystem, plugin.RunAsSystem, true},
		{"system platform + user job runs (via priv-drop)", osadapter.ModeSystem, plugin.RunAsCurrentUser, true},
		{"user platform + user job runs", osadapter.ModeUser, plugin.RunAsCurrentUser, true},
		{"user platform + system job unavailable", osadapter.ModeUser, plugin.RunAsSystem, false},
		{"user platform + empty run_as runs", osadapter.ModeUser, "", true},
		{"system platform + empty run_as runs", osadapter.ModeSystem, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, db := newSchedMode(t, tc.mode)
			p := testutil.ScriptPlugin(t, "x",
				`echo '{"status":"ok"}'`)
			p.Manifest.RunAs = tc.runAs
			j := config.Job{ID: "j1", Plugin: "x", Enabled: true,
				Schedule: "* * * * *", Timeout: dur(2 * time.Second)}
			s.trigger(j, p)

			_, runErr := db.Runs.LastByStatus("j1", state.RunStatusOK)
			didRun := runErr == nil
			if didRun != tc.wantRun {
				t.Errorf("didRun=%v want=%v (err=%v)", didRun, tc.wantRun, runErr)
			}

			// Go-reviewer HIGH + MEDIUM #9: skips do NOT record a DB
			// event (the old behavior produced 288×/day spam at @every
			// 5m), AND a successful run does NOT incorrectly record a
			// "job_skipped_run_as" event. Both cases verified here.
			ev, _ := db.Events.Recent(10)
			for _, e := range ev {
				if e.EventType == "job_skipped_run_as" {
					t.Errorf("unexpected job_skipped_run_as event recorded "+
						"(this event was removed to avoid DB spam): %+v", e)
				}
			}
		})
	}
}

func TestStartStopLifecycle(t *testing.T) {
	s, _ := newSched(t)
	p := testutil.ScriptPlugin(t, "ok", `echo '{"status":"ok"}'`)
	s.Register([]config.Job{{ID: "j1", Plugin: "ok", Enabled: true,
		Schedule: "* * * * *"}}, map[string]plugin.Discovered{"ok": p})
	s.Start()
	ctx := s.Stop()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Error("Stop did not drain in time")
	}
}

// TestStartKickstartsRegisteredJobsImmediately proves the cold-start fix:
// a job on a long interval (whose first cron tick is an hour out) still runs
// once promptly because Start kickstarts every registered job. Without the
// kickstart this job would show status=none/never for an hour after start —
// the exact network-block (@every 30m) blind window this closes.
func TestStartKickstartsRegisteredJobsImmediately(t *testing.T) {
	s, db := newSched(t)
	p := testutil.ScriptPlugin(t, "ok", `echo '{"status":"ok"}'`)
	s.Register([]config.Job{{ID: "j1", Plugin: "ok", Enabled: true,
		Schedule: "@every 1h", Timeout: dur(5 * time.Second)}},
		map[string]plugin.Discovered{"ok": p})

	s.Start()
	t.Cleanup(func() {
		// Bounded so a regressed/hung Stop surfaces fast instead of deadlocking
		// the package until the global test timeout.
		select {
		case <-s.Stop().Done():
		case <-time.After(2 * time.Second):
			t.Error("Stop did not drain in time")
		}
	})

	// The kickstart fires in a goroutine; wait for the run to actually RECORD
	// (TriggerCount increments before the run completes, so poll the DB row).
	deadline := time.After(3 * time.Second)
	for {
		if _, err := db.Runs.LastByStatus("j1", state.RunStatusOK); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("job not kickstarted within 3s (trigger count = %d)", s.TriggerCount("j1"))
		case <-time.After(20 * time.Millisecond):
		}
	}
	// Kickstart fires each job exactly once; the 1h cron tick is far away, so
	// the count must stay at 1 (no accidental double-fire).
	if got := s.TriggerCount("j1"); got != 1 {
		t.Errorf("kickstart trigger count = %d, want exactly 1", got)
	}
}
