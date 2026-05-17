package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// --- fakes ---

type fakeFetch struct {
	latest    string
	latestErr error
}

func (f *fakeFetch) ResolveLatest(context.Context) (string, error) {
	return f.latest, f.latestErr
}
func (f *fakeFetch) EnsureBinary(_ context.Context, st *Store, v string) error {
	p := st.BinPath(v)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte("platform "+v), 0o755)
}

type fakePlat struct {
	running  string
	started  []string
	stopped  int
	crashV   string // version that "crashed quickly"
	healthyV string // version that is "healthy"
}

func (p *fakePlat) RunningVersion() (string, error) { return p.running, nil }
func (p *fakePlat) Start(_, v string) error         { p.started = append(p.started, v); p.running = v; return nil }
func (p *fakePlat) Stop() error                     { p.stopped++; p.running = ""; return nil }
func (p *fakePlat) CrashedQuickly(v string) bool    { return v == p.crashV }
func (p *fakePlat) HealthyFor(v string) bool        { return v == p.healthyV }

func newExec(t *testing.T) (*Executor, *Store, *fakeFetch, *fakePlat) {
	t.Helper()
	st := &Store{Dir: t.TempDir()}
	f := &fakeFetch{}
	p := &fakePlat{}
	return NewExecutor(st, f, p, nil), st, f, p
}

func TestExecutorResolveLatestThenStart(t *testing.T) {
	e, st, f, p := newExec(t)
	f.latest = "v1"

	// 1st tick: no config → resolve latest, write config.
	if a, err := e.Tick(context.Background()); err != nil || a.Kind != ResolveLatest {
		t.Fatalf("tick1 = %+v err=%v", a, err)
	}
	if st.Desired() != "v1" {
		t.Fatalf("desired not written: %q", st.Desired())
	}
	// 2nd tick: config present, nothing running → ensure+start v1.
	if a, err := e.Tick(context.Background()); err != nil || a.Kind != EnsureRunning {
		t.Fatalf("tick2 = %+v err=%v", a, err)
	}
	if p.running != "v1" || !st.HaveBin("v1") {
		t.Fatalf("v1 not started/installed: running=%q bin=%v", p.running, st.HaveBin("v1"))
	}
}

func TestExecutorSteadyAndPromoteGood(t *testing.T) {
	e, st, _, p := newExec(t)
	st.WriteDesired("v1")
	p.running = "v1"
	p.healthyV = "v1"

	a, err := e.Tick(context.Background())
	if err != nil || a.Kind != Steady {
		t.Fatalf("expected steady, got %+v err=%v", a, err)
	}
	if st.Good() != "v1" {
		t.Fatalf("healthy desired must be promoted to good, got %q", st.Good())
	}
}

func TestExecutorCrashLoopMarksBadThenRollback(t *testing.T) {
	e, st, _, p := newExec(t)
	st.WriteDesired("v2")
	st.WriteGood("v1")
	p.running = "v2"
	p.crashV = "v2" // v2 crashes quickly every tick

	// crashThreshold consecutive fast exits → v2 marked bad, and on the
	// same tick Decide rolls back to good v1.
	var last Action
	for i := 0; i < crashThreshold; i++ {
		a, err := e.Tick(context.Background())
		if err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		last = a
	}
	if !st.BadSet()["v2"] {
		t.Fatal("v2 should be marked bad after crash loop")
	}
	if last.Kind != Rollback || last.Target != "v1" {
		t.Fatalf("crash-loop tick should roll back to v1, got %+v", last)
	}
	if p.running != "v1" {
		t.Fatalf("platform should be rolled back to v1, running=%q", p.running)
	}
	// A further tick is now steady on good v1 (desired still bad).
	a, err := e.Tick(context.Background())
	if err != nil || a.Kind != Steady || a.Target != "v1" {
		t.Fatalf("post-rollback should be steady v1, got %+v err=%v", a, err)
	}
}

func TestExecutorSwitchStopsOldStartsNew(t *testing.T) {
	e, st, _, p := newExec(t)
	st.WriteDesired("v2")
	p.running = "v1"

	a, err := e.Tick(context.Background())
	if err != nil || a.Kind != EnsureRunning || a.Target != "v2" {
		t.Fatalf("expected switch to v2, got %+v err=%v", a, err)
	}
	if p.stopped != 1 || p.running != "v2" {
		t.Fatalf("old not stopped / new not started: stopped=%d running=%q", p.stopped, p.running)
	}
}

func TestExecutorObserveErrorPropagates(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	e := NewExecutor(st, &fakeFetch{}, &errPlat{}, nil)
	if _, err := e.Tick(context.Background()); err == nil {
		t.Fatal("observe error must propagate")
	}
}

type errPlat struct{}

func (*errPlat) RunningVersion() (string, error) { return "", os.ErrPermission }
func (*errPlat) Start(string, string) error      { return nil }
func (*errPlat) Stop() error                     { return nil }
func (*errPlat) CrashedQuickly(string) bool      { return false }
func (*errPlat) HealthyFor(string) bool          { return false }
