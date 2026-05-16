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
	"github.com/eliteGoblin/focusd/platform/internal/testutil"
)

func newSched(t *testing.T) (*Scheduler, *state.DB) {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(runner.New(db), db, log), db
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
