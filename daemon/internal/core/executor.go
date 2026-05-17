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
}

// New builds an Executor.
func NewExecutor(st *Store, f Fetcher, p Platform, log *slog.Logger) *Executor {
	return &Executor{Store: st, Fetch: f, Plat: p, Log: log, crashHit: map[string]int{}}
}

const crashThreshold = 3 // consecutive fast exits ⇒ mark version bad

// observe gathers State from disk + the running platform.
func (e *Executor) observe() (State, error) {
	running, err := e.Plat.RunningVersion()
	if err != nil {
		return State{}, fmt.Errorf("observe running: %w", err)
	}
	return State{
		HaveConfig: e.Store.HaveConfig(),
		Desired:    e.Store.Desired(),
		Running:    running,
		Good:       e.Store.Good(),
		Bad:        e.Store.BadSet(),
	}, nil
}

// Tick performs exactly one reconcile step. Returns the Action taken.
func (e *Executor) Tick(ctx context.Context) (Action, error) {
	st, err := e.observe()
	if err != nil {
		return Action{}, err
	}

	// Crash-loop detection on the currently-running version.
	if st.Running != "" {
		if e.Plat.CrashedQuickly(st.Running) {
			e.crashHit[st.Running]++
			if e.crashHit[st.Running] >= crashThreshold {
				_ = e.Store.MarkBad(st.Running)
				e.logf("version %s crash-looped (%d) → marked bad",
					st.Running, e.crashHit[st.Running])
				st.Bad = e.Store.BadSet() // re-read so Decide rolls back
			}
		} else if e.Plat.HealthyFor(st.Running) {
			e.crashHit[st.Running] = 0
			if st.Running == st.Desired && st.Good != st.Running {
				_ = e.Store.WriteGood(st.Running) // promote
			}
		}
	}

	act := Decide(st)
	return act, e.apply(ctx, act)
}

func (e *Executor) apply(ctx context.Context, a Action) error {
	switch a.Kind {
	case ResolveLatest:
		v, err := e.Fetch.ResolveLatest(ctx)
		if err != nil {
			return fmt.Errorf("resolve latest: %w", err)
		}
		e.logf("resolved latest = %s", v)
		return e.Store.WriteDesired(v)

	case EnsureRunning, Rollback:
		v := a.Target
		if !e.Store.HaveBin(v) {
			if err := e.Fetch.EnsureBinary(ctx, e.Store, v); err != nil {
				return fmt.Errorf("ensure binary %s: %w", v, err)
			}
		}
		if cur, _ := e.Plat.RunningVersion(); cur != "" && cur != v {
			if err := e.Plat.Stop(); err != nil {
				return fmt.Errorf("stop %s: %w", cur, err)
			}
		}
		if err := e.Plat.Start(e.Store.BinPath(v), v); err != nil {
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
