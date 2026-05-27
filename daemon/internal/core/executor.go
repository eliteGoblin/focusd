package core

import (
	"context"
	"fmt"
	"log/slog"
)

// Seams — real implementations hit GitHub / launchd / processes; tests
// inject fakes so the whole executor is verified without network/root.
type (
	// Fetcher resolves the latest release and downloads + installs a
	// version's verified binary into the store (Download must
	// Ed25519-verify before placing it; returns error if not genuine).
	Fetcher interface {
		ResolveLatest(ctx context.Context) (string, error)
		EnsureBinary(ctx context.Context, st *Store, version string) error
	}
	// Platform controls the platform process.
	Platform interface {
		// RunningVersion returns the version of the running platform,
		// or "" if none is running.
		RunningVersion() (string, error)
		// Start launches the platform binary at binPath for version v.
		Start(binPath, version string) error
		// Stop terminates the running platform.
		Stop() error
		// CrashedQuickly reports whether the version started last exited
		// within the unhealthy window (used for crash-loop detection).
		CrashedQuickly(version string) bool
		// HealthyFor reports whether version has stayed up long enough
		// to be promoted to "good".
		HealthyFor(version string) bool
	}
)

// Executor runs one reconcile tick: observe → core.Decide → act.
type Executor struct {
	Store    *Store
	Fetch    Fetcher
	Plat     Platform
	Log      *slog.Logger
	crashHit map[string]int // in-memory consecutive fast-exits per version
	// lastTarget is the version this executor last drove the platform
	// to (EnsureRunning/Rollback target). Crash detection keys off this
	// so a version that crashes instantly is still caught.
	lastTarget string
}

// New builds an Executor.
func NewExecutor(st *Store, f Fetcher, p Platform, log *slog.Logger) *Executor {
	return &Executor{Store: st, Fetch: f, Plat: p, Log: log, crashHit: map[string]int{}}
}

const crashThreshold = 3 // consecutive fast exits ⇒ mark version bad

// Tick performs exactly one reconcile step. Returns the Action taken.
func (e *Executor) Tick(ctx context.Context) (Action, error) {
	running, err := e.Plat.RunningVersion()
	if err != nil {
		return Action{}, fmt.Errorf("observe running: %w", err)
	}

	// Crash-loop detection. Check the version we last drove (lastTarget)
	// — NOT the currently-running version — because a version that
	// crashes immediately is no longer "running" yet must still be
	// detected, marked bad, and rolled back.
	cv := e.lastTarget
	if cv == "" {
		cv = running
	}
	if cv != "" {
		switch {
		case e.Plat.CrashedQuickly(cv):
			e.crashHit[cv]++
			if e.crashHit[cv] >= crashThreshold {
				_ = e.Store.MarkBad(cv)
				e.logf("version %s crash-looped (%d) → marked bad", cv, e.crashHit[cv])
			}
		case e.Plat.HealthyFor(cv):
			e.crashHit[cv] = 0
		}
	}

	st := State{
		HaveConfig: e.Store.HaveConfig(),
		Desired:    e.Store.Desired(),
		Running:    running,
		Good:       e.Store.Good(),
		Bad:        e.Store.BadSet(),
	}

	// Promote: a healthy running version that equals desired becomes good.
	if running != "" && running == st.Desired && st.Good != running &&
		e.Plat.HealthyFor(running) {
		_ = e.Store.WriteGood(running)
		st.Good = running
	}

	act := Decide(st)
	if act.Kind == EnsureRunning || act.Kind == Rollback {
		e.lastTarget = act.Target
	}
	return act, e.apply(ctx, act)
}

func (e *Executor) apply(ctx context.Context, a Action) error {
	switch a.Kind {
	case EnsureRunning, Rollback:
		v := a.Target

		// Step 1 — ensure the new binary is on disk AND Ed25519-verified
		// BEFORE we touch the running platform. If the fetch fails (e.g.
		// network outage, a bad release on GitHub), we return the error
		// WITHOUT having stopped anything — the old platform keeps
		// running uninterrupted. Replacement-running invariant first.
		if !e.Store.HaveBin(v) {
			if err := e.Fetch.EnsureBinary(ctx, e.Store, v); err != nil {
				return fmt.Errorf("ensure binary %s: %w", v, err)
			}
		}

		// Step 2 — snapshot the current running version BEFORE stopping
		// it, so a failed start can roll back.
		prevRunning, _ := e.Plat.RunningVersion()

		// Step 3 — only now stop the old, if it's a different version.
		if prevRunning != "" && prevRunning != v {
			if err := e.Plat.Stop(); err != nil {
				return fmt.Errorf("stop %s: %w", prevRunning, err)
			}
		}

		// Step 4 — start the new. If this fails AND we just stopped a
		// previously-running version, roll back to it (its binary is
		// still on disk). Best-effort: even a failed rollback is
		// preferable to silently leaving focusd in a stopped state.
		if err := e.Plat.Start(e.Store.BinPath(v), v); err != nil {
			if prevRunning != "" && prevRunning != v && e.Store.HaveBin(prevRunning) {
				if rbErr := e.Plat.Start(e.Store.BinPath(prevRunning), prevRunning); rbErr == nil {
					e.logf("start %s failed (%v); rolled back to previously-running %s",
						v, err, prevRunning)
				} else {
					e.logf("start %s failed (%v); rollback to %s ALSO failed (%v) — focusd is down",
						v, err, prevRunning, rbErr)
				}
			}
			return fmt.Errorf("start %s: %w", v, err)
		}
		e.logf("%s → running %s", a.Kind, v)
		return nil

	case Steady:
		return nil
	case Blocked:
		e.logf("BLOCKED: %s", a.Note)
		return nil
	}
	return nil
}

func (e *Executor) logf(format string, args ...any) {
	if e.Log != nil {
		e.Log.Info(fmt.Sprintf(format, args...))
	}
}
