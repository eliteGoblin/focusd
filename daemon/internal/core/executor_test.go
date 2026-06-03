package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// --- fakes ---

type fakeFetch struct {
	latest    string
	latestErr error
	// panicOnAny, when true, makes BOTH methods panic. Used by the
	// "reconcile must never touch the network" regression to prove the
	// reconcile path doesn't invoke the fetcher at all when no desired
	// version is configured.
	panicOnAny bool
	// ensureErr, keyed by version, makes EnsureBinary return the given
	// error for that version (and NOT lay down the binary) — covers the
	// "fetch fails before we touch the running platform" invariant.
	ensureErr map[string]error
}

func (f *fakeFetch) ResolveLatest(context.Context) (string, error) {
	if f.panicOnAny {
		panic("fakeFetch.ResolveLatest must not be called from reconcile")
	}
	return f.latest, f.latestErr
}
func (f *fakeFetch) EnsureBinary(_ context.Context, st *Store, v string) error {
	if f.panicOnAny {
		panic("fakeFetch.EnsureBinary must not be called from reconcile")
	}
	if err, ok := f.ensureErr[v]; ok {
		return err
	}
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
	// startErr, keyed by version, makes Start(v) return the given error
	// WITHOUT mutating `running`. Used to drive the rollback-on-start-
	// failure paths in Bug 2.
	startErr map[string]error
	// callLog records every Start("vX") / Stop call in the order they
	// happened so a test can assert exact ordering (e.g. "v2 was tried,
	// then rollback v1"). Always recorded; existing tests don't read it.
	callLog []string
}

func (p *fakePlat) RunningVersion() (string, error) { return p.running, nil }
func (p *fakePlat) Start(_, v string) error {
	p.callLog = append(p.callLog, "start:"+v)
	if err, ok := p.startErr[v]; ok {
		// Do NOT mutate `running`: a failed Start must not pretend the
		// version is up. The executor's rollback path depends on this.
		return err
	}
	p.started = append(p.started, v)
	p.running = v
	return nil
}
func (p *fakePlat) Stop() error {
	p.callLog = append(p.callLog, "stop")
	p.stopped++
	p.running = ""
	return nil
}
func (p *fakePlat) CrashedQuickly(v string) bool { return v == p.crashV }
func (p *fakePlat) HealthyFor(v string) bool     { return v == p.healthyV }

// fakeLock is a ProcessLock stub. acquireOK/acquireErr are the canned
// TryAcquire result; calls counts how many times TryAcquire was invoked so a
// test can assert the lock is acquired exactly once and then held.
type fakeLock struct {
	acquireOK  bool
	acquireErr error
	calls      int
}

func (l *fakeLock) TryAcquire(string) (bool, error) {
	l.calls++
	return l.acquireOK, l.acquireErr
}
func (l *fakeLock) Release() error { return nil }

func newExec(t *testing.T) (*Executor, *Store, *fakeFetch, *fakePlat) {
	t.Helper()
	st := &Store{Dir: t.TempDir()}
	f := &fakeFetch{}
	p := &fakePlat{}
	// Default lock wins (ok=true) so existing tests behave exactly as before.
	return NewExecutor(st, f, p, &fakeLock{acquireOK: true}, nil), st, f, p
}

// With no version config, the reconcile loop must be Blocked — NOT
// auto-resolve via the network. The desired version is pinned out-of-
// band by `daemon install -v` / `daemon update vX.Y.Z`.
func TestExecutorBlockedWithNoDesired(t *testing.T) {
	e, st, _, p := newExec(t)

	if a, err := e.Tick(context.Background()); err != nil || a.Kind != Blocked {
		t.Fatalf("no desired ⇒ Blocked, got %+v err=%v", a, err)
	}
	if st.Desired() != "" {
		t.Fatalf("Blocked tick must NOT write desired: %q", st.Desired())
	}
	if p.running != "" {
		t.Fatalf("Blocked tick must NOT start anything: %q", p.running)
	}
}

// Once the user pins a desired version, the next tick brings the
// platform up via EnsureRunning (fetch-if-missing + start).
func TestExecutorPinnedDesiredStarts(t *testing.T) {
	e, st, _, p := newExec(t)
	if err := st.WriteDesired("v1"); err != nil {
		t.Fatal(err)
	}
	if a, err := e.Tick(context.Background()); err != nil || a.Kind != EnsureRunning {
		t.Fatalf("pinned desired ⇒ EnsureRunning, got %+v err=%v", a, err)
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

// Regression: a version that crashes INSTANTLY (RunningVersion="" right
// away) must still be detected, marked bad, and rolled back — crash
// detection keys off lastTarget, not the running version.
func TestExecutorImmediateCrashStillRollsBack(t *testing.T) {
	e, st, _, p := newExec(t)
	st.WriteDesired("v2")
	st.WriteGood("v1")
	p.running = ""  // v2 crashes instantly → never "running"
	p.crashV = "v2" // CrashedQuickly("v2") == true

	var last Action
	for i := 0; i < crashThreshold+1; i++ {
		a, err := e.Tick(context.Background())
		if err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		last = a
	}
	if !st.BadSet()["v2"] {
		t.Fatal("instantly-crashing v2 must be marked bad")
	}
	if last.Kind != Rollback || last.Target != "v1" || p.running != "v1" {
		t.Fatalf("must roll back to v1, got %+v running=%q", last, p.running)
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
	e := NewExecutor(st, &fakeFetch{}, &errPlat{}, &fakeLock{acquireOK: true}, nil)
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

// --- Bug 3 regression: reconcile tick must NEVER hit the network ---
//
// Pre-fix, a missing version.json drove the executor to call
// Fetch.ResolveLatest every tick → 10s/tick × GitHub's 60/hr unauth cap =
// near-instant 403 loop. The fix returns Blocked from Decide and the
// executor's apply() for Blocked is a no-op. This proves it by wiring a
// Fetcher whose every method PANICS — a single accidental network call
// blows the test up.
func TestExecutorReconcileNeverHitsNetworkWithNoDesired(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	f := &fakeFetch{panicOnAny: true}
	p := &fakePlat{}
	e := NewExecutor(st, f, p, &fakeLock{acquireOK: true}, nil)

	// No WriteDesired call: version.json is absent → HaveConfig == false.
	a, err := e.Tick(context.Background())
	if err != nil {
		t.Fatalf("Blocked tick must not error, got %v", err)
	}
	if a.Kind != Blocked {
		t.Fatalf("no desired ⇒ Blocked, got %+v", a)
	}
	// Belt-and-suspenders: the platform must be untouched too.
	if p.running != "" || len(p.started) != 0 || p.stopped != 0 {
		t.Fatalf("Blocked tick must not mutate platform: running=%q started=%v stopped=%d",
			p.running, p.started, p.stopped)
	}
}

// --- Bug 2 (a): fetch-fail before stop ---
//
// Atomic-install reorder: EnsureBinary BEFORE Stop. A failed fetch for v2
// (download error, signature mismatch, bad release) must leave the
// previously-running v1 untouched — no Stop call, running still v1.
func TestExecutorFetchFailureDoesNotTouchRunningPlatform(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	if err := st.WriteDesired("v2"); err != nil {
		t.Fatal(err)
	}
	// Seed v1 binary on disk so the executor's HaveBin check accurately
	// reflects "v1 is genuinely installed and running".
	if err := os.MkdirAll(filepath.Dir(st.BinPath("v1")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.BinPath("v1"), []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}

	wantErr := errors.New("simulated signature mismatch on v2")
	f := &fakeFetch{ensureErr: map[string]error{"v2": wantErr}}
	p := &fakePlat{running: "v1"}
	e := NewExecutor(st, f, p, &fakeLock{acquireOK: true}, nil)

	_, err := e.Tick(context.Background())
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("expected ensure-binary error to surface, got %v", err)
	}
	// Critical invariant: the running platform was NOT disturbed.
	if p.running != "v1" {
		t.Fatalf("running platform must remain v1 after fetch failure, got %q", p.running)
	}
	if p.stopped != 0 {
		t.Fatalf("Stop must not be called when fetch fails: stopped=%d", p.stopped)
	}
	if len(p.started) != 0 {
		t.Fatalf("Start must not be called when fetch fails: started=%v", p.started)
	}
	if st.HaveBin("v2") {
		t.Fatalf("failed fetch must not leave a v2 binary on disk")
	}
}

// --- Bug 2 (b): rollback on Start failure (rollback succeeds) ---
//
// New desired v2's binary is on disk (HaveBin true → no fetch call), v1 is
// running. Stop(v1) succeeds, Start(v2) fails. Executor must attempt
// Start(v1) rollback so focusd is not left in a stopped state. The
// original Start error is the one returned to the caller.
func TestExecutorStartFailureRollsBackToPrevRunning(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	if err := st.WriteDesired("v2"); err != nil {
		t.Fatal(err)
	}
	// Both binaries are already on disk so EnsureBinary is skipped.
	for _, v := range []string{"v1", "v2"} {
		if err := os.MkdirAll(filepath.Dir(st.BinPath(v)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(st.BinPath(v), []byte(v), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	startV2Err := errors.New("v2 start exec error")
	f := &fakeFetch{}
	p := &fakePlat{
		running:  "v1",
		startErr: map[string]error{"v2": startV2Err},
	}
	e := NewExecutor(st, f, p, &fakeLock{acquireOK: true}, nil)

	_, err := e.Tick(context.Background())
	if err == nil || !errors.Is(err, startV2Err) {
		t.Fatalf("Tick must return the v2 start error, got %v", err)
	}
	// Call ordering: try v2, stop wraps that, rollback to v1.
	wantOrder := []string{"stop", "start:v2", "start:v1"}
	if !reflect.DeepEqual(p.callLog, wantOrder) {
		t.Fatalf("call order = %v, want %v", p.callLog, wantOrder)
	}
	// Rollback Start("v1") succeeded → running is v1 again.
	if p.running != "v1" {
		t.Fatalf("rollback should leave v1 running, got %q", p.running)
	}
}

// --- Bug 2 (c): rollback on Start failure (rollback ALSO fails) ---
//
// Same swap, but Start(v1) fails too. focusd ends up with nothing running
// — that's the worst case but explicitly accepted: returning to the
// caller WITH the original start error so the operator sees the root
// cause, and best-effort logging both errors.
func TestExecutorStartFailureRollbackAlsoFailsLeavesNothingRunning(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	if err := st.WriteDesired("v2"); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"v1", "v2"} {
		if err := os.MkdirAll(filepath.Dir(st.BinPath(v)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(st.BinPath(v), []byte(v), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	startV2Err := errors.New("v2 start exec error")
	startV1Err := errors.New("v1 rollback also failed")
	f := &fakeFetch{}
	p := &fakePlat{
		running: "v1",
		startErr: map[string]error{
			"v2": startV2Err,
			"v1": startV1Err,
		},
	}
	e := NewExecutor(st, f, p, &fakeLock{acquireOK: true}, nil)

	_, err := e.Tick(context.Background())
	// The contract: the ORIGINAL Start error is what bubbles up — the
	// rollback failure is logged best-effort, not wrapped into the
	// returned error.
	if err == nil || !errors.Is(err, startV2Err) {
		t.Fatalf("Tick must return the original v2 start error, got %v", err)
	}
	if errors.Is(err, startV1Err) {
		t.Fatalf("rollback error must NOT replace the original (got %v)", err)
	}
	// Both Start attempts happened, in order, after the Stop.
	wantOrder := []string{"stop", "start:v2", "start:v1"}
	if !reflect.DeepEqual(p.callLog, wantOrder) {
		t.Fatalf("call order = %v, want %v", p.callLog, wantOrder)
	}
	// Nothing ended up running: Stop cleared `running`, neither Start
	// re-populated it.
	if p.running != "" {
		t.Fatalf("both starts failed ⇒ running=\"\", got %q", p.running)
	}
}

// --- Singleton lock: only the daemon that wins the lock starts a platform ---
//
// (a) Lock won (TryAcquire ok=true) ⇒ apply proceeds to Plat.Start, exactly
// as the existing happy path. Also proves the lock is consulted before Start.
func TestExecutorLockWonStartsPlatform(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	if err := st.WriteDesired("v1"); err != nil {
		t.Fatal(err)
	}
	f := &fakeFetch{}
	p := &fakePlat{}
	lk := &fakeLock{acquireOK: true}
	e := NewExecutor(st, f, p, lk, nil)

	a, err := e.Tick(context.Background())
	if err != nil || a.Kind != EnsureRunning {
		t.Fatalf("lock won ⇒ EnsureRunning, got %+v err=%v", a, err)
	}
	if p.running != "v1" {
		t.Fatalf("lock won ⇒ platform must start, running=%q", p.running)
	}
	if lk.calls != 1 {
		t.Fatalf("lock must be acquired once, calls=%d", lk.calls)
	}
}

// (b) Regression — the false-rollback guard. The loser daemon (TryAcquire
// ok=false) must yield: apply returns nil, Plat.Start is NEVER called, nothing
// is stopped, and across crashThreshold consecutive ticks the crash counter /
// MarkBad is NEVER hit. Because the loser launches no child, there is no
// phantom exit for the uptime-based crash detector to misread as a crash.
func TestExecutorLockLostYieldsAndNeverRollsBack(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	if err := st.WriteDesired("v2"); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteGood("v1"); err != nil {
		t.Fatal(err)
	}
	f := &fakeFetch{}
	p := &fakePlat{} // loser observes nothing running and starts nothing
	lk := &fakeLock{acquireOK: false}
	e := NewExecutor(st, f, p, lk, nil)

	for i := 0; i < crashThreshold; i++ {
		a, err := e.Tick(context.Background())
		if err != nil {
			t.Fatalf("tick %d: yield must not error, got %v", i, err)
		}
		if a.Kind != EnsureRunning {
			t.Fatalf("tick %d: Decide still wants EnsureRunning, got %+v", i, a)
		}
	}
	if len(p.started) != 0 || p.stopped != 0 {
		t.Fatalf("loser must not touch platform: started=%v stopped=%d", p.started, p.stopped)
	}
	if st.BadSet()["v2"] {
		t.Fatal("loser must NEVER mark v2 bad — no child means no phantom crash")
	}
	if got := e.crashHit["v2"]; got != 0 {
		t.Fatalf("loser must never accrue crash hits, got crashHit[v2]=%d", got)
	}
	// The loser must re-try the lock EVERY tick (it never sets holdsLock), so
	// it takes over the instant the holder dies. If holdsLock were wrongly set
	// on a yield, calls would stop at 1 and the loser would be silently
	// promoted on a future acquire.
	if lk.calls != crashThreshold {
		t.Fatalf("loser must re-try lock every tick: calls=%d want %d", lk.calls, crashThreshold)
	}
}

// (c) The lock is acquired ONCE then held — subsequent ticks do not re-acquire.
func TestExecutorLockHeldAfterFirstAcquire(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	if err := st.WriteDesired("v1"); err != nil {
		t.Fatal(err)
	}
	f := &fakeFetch{}
	p := &fakePlat{}
	lk := &fakeLock{acquireOK: true}
	e := NewExecutor(st, f, p, lk, nil)

	for i := 0; i < 3; i++ {
		if _, err := e.Tick(context.Background()); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	if lk.calls != 1 {
		t.Fatalf("lock must be acquired once and held, calls=%d", lk.calls)
	}
}

// (d) A real I/O failure from TryAcquire surfaces as an error from apply.
func TestExecutorLockAcquireErrorPropagates(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	if err := st.WriteDesired("v1"); err != nil {
		t.Fatal(err)
	}
	f := &fakeFetch{}
	p := &fakePlat{}
	wantErr := errors.New("flock I/O failure")
	lk := &fakeLock{acquireErr: wantErr}
	e := NewExecutor(st, f, p, lk, nil)

	_, err := e.Tick(context.Background())
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("lock error must propagate, got %v", err)
	}
	if len(p.started) != 0 {
		t.Fatalf("lock error ⇒ platform must not start, started=%v", p.started)
	}
}
