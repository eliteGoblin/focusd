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
	"github.com/eliteGoblin/focusd/platform/internal/core/state"
	"github.com/robfig/cron/v3"
)

// Scheduler owns the cron engine and job→plugin bindings.
type Scheduler struct {
	cron *cron.Cron
	run  *runner.Runner
	db   *state.DB
	log  *slog.Logger

	mu        sync.Mutex
	triggered map[string]int // jobID -> trigger count (test/observability)
}

// New builds a scheduler. The runner and DB must be ready.
func New(r *runner.Runner, db *state.DB, log *slog.Logger) *Scheduler {
	return &Scheduler{
		cron:      cron.New(),
		run:       r,
		db:        db,
		log:       log,
		triggered: map[string]int{},
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

// trigger runs one job occurrence, enforcing no-overlap.
func (s *Scheduler) trigger(j config.Job, p plugin.Discovered) {
	s.mu.Lock()
	s.triggered[j.ID]++
	s.mu.Unlock()

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
