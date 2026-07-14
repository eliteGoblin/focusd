// Package scheduler registers enabled job plugins on a cron schedule and
// triggers the runner, enforcing no-overlap execution via job_locks.
package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/eliteGoblin/focusd/platform/internal/core/config"
	"github.com/eliteGoblin/focusd/platform/internal/core/plugin"
	"github.com/eliteGoblin/focusd/platform/internal/core/runner"
	"github.com/eliteGoblin/focusd/platform/internal/core/snapshot"
	"github.com/eliteGoblin/focusd/platform/internal/core/state"
	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
	"github.com/robfig/cron/v3"
)

// Scheduler owns the cron engine and job→plugin bindings.
type Scheduler struct {
	cron *cron.Cron
	run  *runner.Runner
	db   *state.DB
	log  *slog.Logger
	mode osadapter.RunMode // platform run mode; gates plugin run_as
	// snap mirrors scheduler-recorded terminal runs (skipped / unavailable)
	// into the status snapshot. The runner mirrors the runs it records; the
	// scheduler mirrors the ones IT records before the runner is reached. A
	// nil store is a no-op, so existing New(...) callers/tests are unaffected.
	snap *snapshot.Store

	mu         sync.Mutex
	triggered  map[string]int  // jobID -> trigger count (test/observability)
	skipLogged map[string]bool // jobID -> Info-logged "first run_as skip"
}

// New builds a scheduler. The runner and DB must be ready. mode is the
// platform's run mode; it gates dispatch via CanDispatch. A system-mode
// platform serves current_user plugins through the runner's runtime
// privilege-drop (it does NOT run them as root); a user-mode platform
// cannot serve system plugins and marks them unavailable.
func New(r *runner.Runner, db *state.DB, log *slog.Logger, mode osadapter.RunMode) *Scheduler {
	return &Scheduler{
		cron:       cron.New(),
		run:        r,
		db:         db,
		log:        log,
		mode:       mode,
		triggered:  map[string]int{},
		skipLogged: map[string]bool{},
	}
}

// WithSnapshot wires the scheduler to mirror the terminal runs IT records
// (no-overlap skips, mode-unavailable rows) into the status snapshot. A nil
// store leaves it a no-op writer. Returns the same *Scheduler for chaining.
func (s *Scheduler) WithSnapshot(snap *snapshot.Store) *Scheduler {
	s.snap = snap
	return s
}

// recordSnapshot mirrors one scheduler-recorded terminal run into the status
// snapshot. Best-effort: the DB row is the source of truth, so a snapshot
// write failure is logged and swallowed. nil-safe via the Store's receiver.
func (s *Scheduler) recordSnapshot(jobID, status string) {
	if err := s.snap.Record(jobID, status, time.Now()); err != nil {
		s.log.Warn("status snapshot write failed", "job", jobID)
	}
}

// CanDispatch reports whether the platform's run mode can dispatch a
// plugin with the given run_as. The semantics are "can this mode serve
// this plugin", NOT "do they match" — a system platform serves BOTH its
// own system plugins (native, as root) AND current_user plugins (via the
// runner's fork→setuid privilege-drop). FEATURE 08:
//
//   - system platform: CAN dispatch system (native) AND current_user
//     (priv-drop to the console user). The runner handles the drop and,
//     if no console user is logged in, defers the tick as "unavailable".
//   - user platform: CAN dispatch current_user (native — it IS the user)
//     but CANNOT dispatch system (no escalation). System plugins are
//     reported UNAVAILABLE, not failed: reinstall with admin for them.
//   - "" (legacy/unknown run_as): always dispatchable (no gate).
//
// A false return is NOT an error — it means "this plugin needs a
// different install mode"; the caller records an unavailable run.
func CanDispatch(runAs string, mode osadapter.RunMode) bool {
	switch runAs {
	case "":
		return true // legacy behavior: no gate
	case plugin.RunAsSystem:
		// Only a system (root) platform can run a system plugin. A user
		// platform cannot escalate → unavailable.
		return mode == osadapter.ModeSystem
	case plugin.RunAsCurrentUser, plugin.RunAsActiveUser:
		// Both modes can serve a current_user plugin: user natively, system
		// via runtime privilege-drop in the runner.
		return mode == osadapter.ModeUser || mode == osadapter.ModeSystem
	default:
		// Unknown values shouldn't reach here (manifest validation
		// rejects them), but be conservative: do not dispatch.
		return false
	}
}

// Register binds enabled jobs to schedules. A job is skipped (with a
// recorded platform_event) when its plugin is missing or rejected, or
// its cron expression is invalid. Returns the count registered.
func (s *Scheduler) Register(jobs []config.Job, plugins map[string]plugin.Discovered) (int, error) {
	registered := 0
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		p, ok := plugins[j.Plugin]
		if !ok || !p.OK {
			reason := fmt.Sprintf("job %q references unavailable plugin %q", j.ID, j.Plugin)
			s.event(state.SeverityWarn, "job_not_registered", reason, j.ID)
			s.log.Warn("job not registered", "job", j.ID, "plugin", j.Plugin)
			continue
		}

		if err := s.persistJob(j, p); err != nil {
			return registered, err
		}

		job := j // capture
		disc := p
		_, err := s.cron.AddFunc(job.Schedule, func() {
			s.trigger(job, disc)
		})
		if err != nil {
			reason := fmt.Sprintf("invalid schedule %q for job %q: %v", job.Schedule, job.ID, err)
			s.event(state.SeverityError, "bad_schedule", reason, job.ID)
			s.log.Error("bad schedule", "job", job.ID, "err", err)
			continue
		}
		registered++
		s.log.Info("job registered", "job", job.ID, "plugin", job.Plugin, "schedule", job.Schedule)
	}
	return registered, nil
}

// RegisterIntegritySweep adds one synthetic @every <interval> entry that runs
// the whole-bundle integrity reconcile — the IDLE BACKSTOP (ADR-0019 / FEATURE
// 23, Fix 4). The runner's point-of-use verify is the PER-SCHEDULED-RUN
// guarantee: it fires immediately before every job that actually runs, so a
// swap of a running plugin's binary is caught and repaired before exec. This
// sweep covers the gap the point-of-use check cannot reach — plugins that are
// idle or disabled, whose binaries would otherwise never be re-verified — so a
// tamper of a non-running plugin self-heals within ≤1 sweep interval instead of
// waiting for a restart.
//
// interval is the sweep cadence (config.Platform.IntegritySweepInterval);
// values <= 0 fall back to the 1m default so a mis-set config can't disable the
// backstop. sweep is the bundle ExtractTo call (idempotent / churn-free); on a
// non-nil error it records an integrity_sweep_failed event (SeverityError) so a
// wedged sweep can't hide behind a green status. Errors registering the cron
// entry are returned to fail the build loudly.
func (s *Scheduler) RegisterIntegritySweep(interval time.Duration, sweep func() error) error {
	if interval <= 0 {
		interval = config.DefaultSweepInterval
	}
	schedule := "@every " + interval.String()
	_, err := s.cron.AddFunc(schedule, func() {
		if err := sweep(); err != nil {
			s.event(state.SeverityError, state.EventIntegritySweepFailed,
				"plugin integrity sweep failed", "integrity-sweep")
			s.log.Error("integrity sweep failed", "err", err)
		}
	})
	if err != nil {
		return fmt.Errorf("register integrity sweep: %w", err)
	}
	s.log.Info("integrity sweep registered", "schedule", schedule)
	return nil
}

// trigger runs one job occurrence, enforcing no-overlap.
func (s *Scheduler) trigger(j config.Job, p plugin.Discovered) {
	s.mu.Lock()
	s.triggered[j.ID]++
	s.mu.Unlock()

	// can-dispatch gate: if this platform's mode cannot serve the plugin's
	// run_as, the job is UNAVAILABLE in this install — not a failed run.
	// The only such case is a `system` plugin under a user-mode platform
	// (no escalation). A system platform serves current_user plugins via
	// the runner's privilege-drop, so they pass this gate.
	//
	// We record ONE "unavailable" run row (queryable by a future `daemon
	// status` to show "requires system-mode install") then keep quiet: at
	// @every 5m a gated plugin fires 288×/day, so we do NOT record a DB
	// event or a fresh row per tick — only the first occurrence per (job,
	// lifetime) gets an Info log + the unavailable row; subsequent ticks
	// log at Debug and skip silently. (Go-reviewer HIGH dedup pattern.)
	if p.Manifest != nil && !CanDispatch(p.Manifest.RunAs, s.mode) {
		if s.logFirstSkip(j.ID, p.Manifest.RunAs) {
			reason := fmt.Sprintf("plugin run_as=%q unavailable under %s-mode install (reinstall with admin for full coverage)",
				p.Manifest.RunAs, string(s.mode))
			// Surface a failed write — the "unavailable" row is what a
			// future `daemon status` reads to show the plugin as inactive;
			// silently losing it would make status lie. (Go-reviewer MEDIUM.)
			if rerr := s.db.Runs.RecordUnavailable(j.ID, j.Plugin, reason); rerr != nil {
				s.log.Warn("record unavailable failed", "job", j.ID, "err", rerr)
			} else {
				s.recordSnapshot(j.ID, state.RunStatusUnavailable)
			}
		}
		s.log.Debug("job unavailable (run_as not servable in this mode)",
			"job", j.ID, "run_as", p.Manifest.RunAs, "mode", string(s.mode))
		return
	}

	if !j.AllowOverlap {
		// Lock TTL = timeout + slack so a crashed run self-heals.
		ttl := j.Timeout.Std() + 30*time.Second
		if ttl <= 0 {
			ttl = time.Minute
		}
		ok, err := s.db.Locks.TryAcquire(j.ID, 0, ttl)
		if err != nil {
			s.log.Error("lock acquire failed", "job", j.ID, "err", err)
			return
		}
		if !ok {
			_ = s.db.Runs.RecordSkipped(j.ID, j.Plugin, "previous run still active (no-overlap)")
			s.recordSnapshot(j.ID, state.RunStatusSkipped)
			s.event(state.SeverityInfo, "job_skipped", "no-overlap: previous run active", j.ID)
			s.log.Info("job skipped (no-overlap)", "job", j.ID)
			return
		}
		defer s.db.Locks.Release(j.ID)
	}

	rj := runner.Job{
		ID:      j.ID,
		Timeout: j.Timeout.Std(),
		Retry:   j.Retry,
		Config:  j.Config,
	}
	out, err := s.run.Run(context.Background(), rj, p, "scheduler")
	if err != nil {
		s.log.Error("job run error", "job", j.ID, "err", err)
		s.event(state.SeverityError, "job_run_error", err.Error(), j.ID)
		return
	}
	s.log.Info("job finished", "job", j.ID, "status", out.Status,
		"exit", out.ExitCode, "ms", out.DurationMS, "attempts", out.Attempts)
}

func (s *Scheduler) persistJob(j config.Job, p plugin.Discovered) error {
	hash := ""
	if b, err := json.Marshal(j.Config); err == nil {
		sum := sha256.Sum256(b) // real content digest (change marker)
		hash = hex.EncodeToString(sum[:])
	}
	if err := s.db.Jobs.Upsert(state.Job{
		ID: j.ID, PluginID: j.Plugin, Enabled: j.Enabled, Schedule: j.Schedule,
		TimeoutMS: j.Timeout.Std().Milliseconds(), Retry: j.Retry,
		AllowOverlap: j.AllowOverlap, ConfigHash: hash,
	}); err != nil {
		return err
	}
	row := plugin.ToInventoryRow(p)
	row.Enabled = true
	return s.db.Plugins.Upsert(row)
}

// logFirstSkip emits a single Info-level "unavailable in this mode" the
// first time a given job is gated this process lifetime, so a startup-log
// inspector can confirm gating; subsequent skips for the same job log at
// Debug only. Returns true on that first occurrence so the caller records
// exactly one "unavailable" run row per (job, lifetime) — not 288×/day.
func (s *Scheduler) logFirstSkip(jobID, runAs string) bool {
	s.mu.Lock()
	first := !s.skipLogged[jobID]
	s.skipLogged[jobID] = true
	s.mu.Unlock()
	if first {
		s.log.Info("job unavailable in this install mode; silencing further occurrences",
			"job", jobID, "run_as", runAs, "mode", string(s.mode))
	}
	return first
}

func (s *Scheduler) event(sev, typ, msg, jobID string) {
	details, _ := json.Marshal(map[string]string{"job_id": jobID})
	_ = s.db.Events.Record(sev, typ, msg, string(details))
}

// Start begins the cron loop (non-blocking).
func (s *Scheduler) Start() { s.cron.Start() }

// Stop halts scheduling and waits for in-flight jobs to finish.
func (s *Scheduler) Stop() context.Context { return s.cron.Stop() }

// TriggerCount reports how many times a job has been triggered (test
// and observability aid).
func (s *Scheduler) TriggerCount(jobID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.triggered[jobID]
}
