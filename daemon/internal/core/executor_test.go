package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
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
	// ensureCalls counts EnsureBinary invocations so the fetch-cooldown
	// regression can assert the fetch is throttled, not re-tried per tick.
	ensureCalls int
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
	f.ensureCalls++
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
	// pid is the value RunningPID reports — the survivor PID the FEATURE 25
	// reap exempts. 0 (the default) means "no live platform to exempt", which
	// makes the executor skip reaping entirely.
	pid int
}

func (p *fakePlat) RunningVersion() (string, error) { return p.running, nil }
func (p *fakePlat) RunningPID() int                 { return p.pid }
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

// --- FIX 1 fakes: content-addressed genuine-vs-tampered binary ---
//
// The daemon's real fetcher Ed25519-verifies a release before placing it and
// the real VerifyBin is sig.VerifyFile. Here a known-good byte marker stands
// in for "genuine": genuineFetch always lays down the marker (a verified
// install), and contentVerify declares a binary genuine iff its bytes are
// exactly the marker — so a fake or empty (in-place-tampered) binary fails,
// exactly as a real signature check would.
const genuineBin = "GENUINE-PLATFORM-BINARY"

type genuineFetch struct{ calls int }

func (genuineFetch) ResolveLatest(context.Context) (string, error) { return "", nil }
func (g *genuineFetch) EnsureBinary(_ context.Context, st *Store, v string) error {
	g.calls++
	p := st.BinPath(v)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(genuineBin), 0o755)
}

func contentVerify(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return string(b) == genuineBin, nil
}

// capturePlat records the on-disk CONTENT of the binary at the instant of each
// Start, so a test can prove the GENUINE bytes were exec'd and the fake never.
type capturePlat struct {
	running     string
	startedWith []string
}

func (p *capturePlat) RunningVersion() (string, error) { return p.running, nil }
func (p *capturePlat) Start(binPath, v string) error {
	b, _ := os.ReadFile(binPath)
	p.startedWith = append(p.startedWith, string(b))
	p.running = v
	return nil
}
func (p *capturePlat) Stop() error                { p.running = ""; return nil }
func (p *capturePlat) CrashedQuickly(string) bool { return false }
func (p *capturePlat) HealthyFor(string) bool     { return false }

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestExecutorRevertsInPlaceTamperedBinaryBeforeExec is the FIX 1 must-hold:
// an in-place-tampered platform binary (fake script OR empty file, path kept)
// is REVERTED to the genuine signed release and the fake NEVER runs. This
// closes the confirmed gap — HaveBin was a bare os.Stat, so a tampered binary
// was exec'd unverified; now the on-disk binary is signature-re-verified
// before every start and a mismatch is dropped + re-fetched (verify-before-exec).
func TestExecutorRevertsInPlaceTamperedBinaryBeforeExec(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(path string) error
	}{
		{"fake-script", func(p string) error { return os.WriteFile(p, []byte("#!/bin/sh\necho pwned\nexit 0\n"), 0o755) }},
		{"empty", func(p string) error { return os.WriteFile(p, nil, 0o755) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &Store{Dir: t.TempDir()}
			if err := st.WriteDesired("v1"); err != nil {
				t.Fatal(err)
			}
			f := &genuineFetch{}
			p := &capturePlat{}
			e := NewExecutor(st, f, p, &fakeLock{acquireOK: true}, nil)
			e.VerifyBin = contentVerify // stand-in for sig.VerifyFile

			// Tick 1: bring v1 up from nothing — one fetch + start the genuine binary.
			if _, err := e.Tick(context.Background()); err != nil {
				t.Fatalf("tick 1: %v", err)
			}
			if p.running != "v1" || f.calls != 1 {
				t.Fatalf("v1 must start via exactly one fetch: running=%q calls=%d", p.running, f.calls)
			}
			if got := mustReadFile(t, st.BinPath("v1")); got != genuineBin {
				t.Fatalf("on-disk binary must be genuine after install, got %q", got)
			}

			// TAMPER in place: swap the bytes, keep the path; child dies.
			if err := tc.tamper(st.BinPath("v1")); err != nil {
				t.Fatal(err)
			}
			p.running = ""
			if mustReadFile(t, st.BinPath("v1")) == genuineBin {
				t.Fatal("pre-condition: tamper must have changed the on-disk bytes")
			}

			// Tick 2: the tampered binary must be REVERTED (re-fetched + verified)
			// and the GENUINE binary started — the fake is never exec'd.
			a, err := e.Tick(context.Background())
			if err != nil {
				t.Fatalf("tick 2: %v", err)
			}
			if a.Kind != EnsureRunning || a.Target != "v1" {
				t.Fatalf("tampered binary ⇒ EnsureRunning v1, got %+v", a)
			}
			if f.calls != 2 {
				t.Fatalf("tampered binary must trigger a re-fetch: fetch calls=%d (want 2)", f.calls)
			}
			if got := mustReadFile(t, st.BinPath("v1")); got != genuineBin {
				t.Fatalf("tampered binary must be reverted to genuine, got %q", got)
			}
			// The crux: every Start ever exec'd the GENUINE bytes — the fake never ran.
			for i, c := range p.startedWith {
				if c != genuineBin {
					t.Fatalf("Start #%d exec'd non-genuine bytes %q — the fake RAN", i, c)
				}
			}
			if p.running != "v1" {
				t.Fatalf("genuine v1 must be running after revert, got %q", p.running)
			}
		})
	}
}

// TestExecutorRefusesTamperedRollbackTarget (FIX 1, verified rollback): when
// Start(target) fails, the executor must NOT roll back onto a prevRunning
// binary that fails its signature check — a tampered idle binary must never be
// exec'd via the rollback path.
func TestExecutorRefusesTamperedRollbackTarget(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	if err := st.WriteDesired("v2"); err != nil {
		t.Fatal(err)
	}
	// v1 (prev) present but TAMPERED; v2 present + genuine but its Start fails.
	for _, v := range []string{"v1", "v2"} {
		if err := os.MkdirAll(filepath.Dir(st.BinPath(v)), 0o755); err != nil {
			t.Fatal(err)
		}
		content := genuineBin
		if v == "v1" {
			content = "TAMPERED-v1"
		}
		if err := os.WriteFile(st.BinPath(v), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	startV2Err := errors.New("v2 start exec error")
	p := &fakePlat{running: "v1", startErr: map[string]error{"v2": startV2Err}}
	e := NewExecutor(st, &genuineFetch{}, p, &fakeLock{acquireOK: true}, nil)
	e.VerifyBin = contentVerify // v1 tampered ⇒ not genuine; v2 genuine

	_, err := e.Tick(context.Background())
	if err == nil || !errors.Is(err, startV2Err) {
		t.Fatalf("Tick must return the v2 start error, got %v", err)
	}
	// The tampered v1 must NEVER be started via rollback — call order stops at
	// stop → start:v2 (no start:v1).
	if !reflect.DeepEqual(p.callLog, []string{"stop", "start:v2"}) {
		t.Fatalf("rollback to tampered v1 must be refused, callLog=%v", p.callLog)
	}
}

// TestExecutorReVerifiesImmediatelyBeforeExec (FIX 1, TOCTOU): a binary that
// passes the Step-1 check but is swapped to a fake before exec must NOT be
// started — the pre-exec re-check (Step 3.5) catches the swap.
func TestExecutorReVerifiesImmediatelyBeforeExec(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	if err := st.WriteDesired("v1"); err != nil {
		t.Fatal(err)
	}
	// v1 present + genuine so Step 1 passes without a fetch.
	if err := os.MkdirAll(filepath.Dir(st.BinPath("v1")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.BinPath("v1"), []byte(genuineBin), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &fakePlat{}
	e := NewExecutor(st, &genuineFetch{}, p, &fakeLock{acquireOK: true}, nil)
	// Genuine on the FIRST check (Step 1), NOT genuine on the SECOND
	// (Step 3.5) — simulating a swap inside the TOCTOU window.
	calls := 0
	e.VerifyBin = func(string) (bool, error) { calls++; return calls == 1, nil }

	if _, err := e.Tick(context.Background()); err == nil {
		t.Fatal("a binary swapped after the Step-1 check must fail the pre-exec re-check")
	}
	if len(p.started) != 0 {
		t.Fatalf("swapped binary must NOT be exec'd, started=%v", p.started)
	}
	if calls < 2 {
		t.Fatalf("expected a second (pre-exec) check call, got %d", calls)
	}
}

// TestExecutorClearingBadSetRecoversWithoutProcessRestart is the FIX 1
// defense-in-depth: clearing the on-disk bad set must also clear the in-memory
// crash state so a wedged version recovers WITHOUT a daemon process restart.
func TestExecutorClearingBadSetRecoversWithoutProcessRestart(t *testing.T) {
	e, st, _, p := newExec(t)
	st.WriteDesired("v2")
	st.WriteGood("v1")
	p.running = "v2"
	p.crashV = "v2" // v2 crash-loops

	// Drive the crash loop → v2 marked bad, rolled back to good v1, crashHit latched.
	for i := 0; i < crashThreshold; i++ {
		if _, err := e.Tick(context.Background()); err != nil {
			t.Fatalf("crash tick %d: %v", i, err)
		}
	}
	if !st.BadSet()["v2"] || e.crashHit["v2"] < crashThreshold {
		t.Fatalf("pre-condition: v2 bad with latched crashHit, bad=%v hits=%d",
			st.BadSet()["v2"], e.crashHit["v2"])
	}

	// Genuine v2 restored: clear the on-disk bad marker + stop crashing. SAME
	// executor instance — NO process restart.
	if err := st.ClearBad("v2"); err != nil {
		t.Fatal(err)
	}
	p.crashV = ""

	a, err := e.Tick(context.Background())
	if err != nil {
		t.Fatalf("recovery tick: %v", err)
	}
	// In-memory crash state mirrored the on-disk clear...
	if e.crashHit["v2"] != 0 {
		t.Fatalf("crashHit[v2] must reset when the bad set is cleared, got %d", e.crashHit["v2"])
	}
	// ...and the daemon drives v2 again instead of staying wedged on good v1.
	if a.Kind != EnsureRunning || a.Target != "v2" || p.running != "v2" {
		t.Fatalf("cleared v2 must be driven again: act=%+v running=%q", a, p.running)
	}
}

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

// allowVerify is the permissive signature-verify stub for tests whose fake
// fetcher writes UNSIGNED stand-in binaries: it declares every on-disk binary
// genuine so the anti-tamper re-fetch gate does not fire spuriously. The
// dedicated anti-tamper tests inject their own content-aware verifier instead.
func allowVerify(string) (bool, error) { return true, nil }

func newExec(t *testing.T) (*Executor, *Store, *fakeFetch, *fakePlat) {
	t.Helper()
	st := &Store{Dir: t.TempDir()}
	f := &fakeFetch{}
	p := &fakePlat{}
	// Default lock wins (ok=true) so existing tests behave exactly as before.
	e := NewExecutor(st, f, p, &fakeLock{acquireOK: true}, nil)
	e.VerifyBin = allowVerify // unsigned fake binaries are treated as genuine
	return e, st, f, p
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

// FEATURE 17 Item 1: with NO desired version on disk but a baked Fallback,
// the tick adopts the fallback — re-pins it to the store (recreating a wiped
// workdir) and drives EnsureRunning, NOT Blocked. This is the wiped-workdir
// self-heal.
func TestExecutorFallbackAdoptedWhenNoDesired(t *testing.T) {
	e, st, _, p := newExec(t)
	e.Fallback = "v9"

	a, err := e.Tick(context.Background())
	if err != nil {
		t.Fatalf("fallback tick must not error, got %v", err)
	}
	if a.Kind != EnsureRunning || a.Target != "v9" {
		t.Fatalf("no desired + fallback ⇒ EnsureRunning v9, got %+v", a)
	}
	if st.Desired() != "v9" {
		t.Fatalf("fallback must be persisted to the store, got desired=%q", st.Desired())
	}
	if p.running != "v9" || !st.HaveBin("v9") {
		t.Fatalf("fallback v9 must be brought up: running=%q bin=%v", p.running, st.HaveBin("v9"))
	}
}

// Floor-not-ceiling: an explicit on-disk desired always wins; the fallback is
// never consulted when a version is already pinned (even an older-looking one).
func TestExecutorExplicitDesiredWinsOverFallback(t *testing.T) {
	e, st, _, p := newExec(t)
	e.Fallback = "v9"
	if err := st.WriteDesired("v1"); err != nil {
		t.Fatal(err)
	}

	a, err := e.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if a.Target != "v1" || p.running != "v1" {
		t.Fatalf("explicit desired v1 must win over fallback v9, got %+v running=%q", a, p.running)
	}
	if st.Desired() != "v1" {
		t.Fatalf("fallback must not overwrite an explicit desired, got %q", st.Desired())
	}
}

// No desired AND no fallback ⇒ still Blocked (the safe default is preserved
// when no fallback is baked in).
func TestExecutorNoFallbackStillBlocked(t *testing.T) {
	e, st, _, p := newExec(t)
	// newExec leaves Fallback == "".
	a, err := e.Tick(context.Background())
	if err != nil || a.Kind != Blocked {
		t.Fatalf("no desired + no fallback ⇒ Blocked, got %+v err=%v", a, err)
	}
	if st.Desired() != "" {
		t.Fatalf("Blocked tick must not write desired, got %q", st.Desired())
	}
	if p.running != "" {
		t.Fatalf("Blocked tick must start nothing, got %q", p.running)
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

// ADR-0015 defense-in-depth: a platform-binary fetch that fails must NOT
// be re-attempted on every ~2s tick. The cooldown defers the retry; once
// it elapses the fetch is attempted again (and on success the binary lands
// and the platform starts).
func TestExecutorFetchFailureBacksOff(t *testing.T) {
	e, st, f, p := newExec(t)
	st.WriteDesired("v1")
	p.running = ""

	// Drive a controllable clock.
	clk := time.Unix(1_000_000, 0)
	e.now = func() time.Time { return clk }

	// First tick: fetch fails → cooldown armed, nothing started.
	f.ensureErr = map[string]error{"v1": errors.New("network down")}
	if _, err := e.Tick(context.Background()); err == nil {
		t.Fatal("first fetch failure must surface an error")
	}
	if f.ensureCalls != 1 {
		t.Fatalf("expected 1 fetch attempt, got %d", f.ensureCalls)
	}

	// Subsequent ticks BEFORE the cooldown elapses must NOT re-fetch.
	clk = clk.Add(10 * time.Second) // < 30s cooldown
	if _, err := e.Tick(context.Background()); err == nil {
		t.Fatal("deferred tick still has no binary → still errors")
	}
	if f.ensureCalls != 1 {
		t.Fatalf("fetch must be throttled within cooldown: attempts=%d (want 1)", f.ensureCalls)
	}

	// After the cooldown elapses, the fetch is retried — now it succeeds,
	// the binary lands, and the platform starts.
	clk = clk.Add(25 * time.Second) // total 35s > 30s cooldown
	f.ensureErr = nil               // recovery
	a, err := e.Tick(context.Background())
	if err != nil {
		t.Fatalf("post-cooldown tick: %v", err)
	}
	if f.ensureCalls != 2 {
		t.Fatalf("fetch must be retried after cooldown: attempts=%d (want 2)", f.ensureCalls)
	}
	if a.Kind != EnsureRunning || p.running != "v1" || !st.HaveBin("v1") {
		t.Fatalf("after recovery v1 must start: act=%+v running=%q bin=%v", a, p.running, st.HaveBin("v1"))
	}
}

// TestExecutorFetchCooldownScopedToVersion proves the cooldown is keyed to the
// version that FAILED, not applied globally: pinning a different version after
// a failure must fetch it immediately, even within the prior version's cooldown
// window (Copilot review on PR #54).
func TestExecutorFetchCooldownScopedToVersion(t *testing.T) {
	e, st, f, p := newExec(t)
	st.WriteDesired("v1")
	p.running = ""

	clk := time.Unix(1_000_000, 0)
	e.now = func() time.Time { return clk }

	// v1 fetch fails → cooldown armed for v1.
	f.ensureErr = map[string]error{"v1": errors.New("network down")}
	if _, err := e.Tick(context.Background()); err == nil {
		t.Fatal("first fetch failure must surface an error")
	}
	if f.ensureCalls != 1 {
		t.Fatalf("expected 1 fetch attempt, got %d", f.ensureCalls)
	}

	// Operator re-pins to v2, still WITHIN v1's cooldown window. v2 must NOT
	// be deferred by v1's cooldown — it fetches immediately and starts.
	clk = clk.Add(5 * time.Second) // << 30s cooldown
	st.WriteDesired("v2")
	f.ensureErr = nil // v2 fetch succeeds
	a, err := e.Tick(context.Background())
	if err != nil {
		t.Fatalf("v2 tick within v1 cooldown must not be deferred: %v", err)
	}
	if f.ensureCalls != 2 {
		t.Fatalf("v2 fetch must be attempted immediately: attempts=%d (want 2)", f.ensureCalls)
	}
	if a.Kind != EnsureRunning || p.running != "v2" || !st.HaveBin("v2") {
		t.Fatalf("v2 must start despite v1 cooldown: act=%+v running=%q bin=%v", a, p.running, st.HaveBin("v2"))
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
	e.VerifyBin = allowVerify // pre-placed bins are unsigned stand-ins → genuine

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
	e.VerifyBin = allowVerify // pre-placed bins are unsigned stand-ins → genuine

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
	e.VerifyBin = allowVerify // unsigned fake binaries are treated as genuine

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
	e.VerifyBin = allowVerify // unsigned fake binaries are treated as genuine

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

// --- FEATURE 25: continuous foreign-platform reap wiring ------------------

// TestExecutorWinnerReapsForeign: the lock WINNER reaps foreign platforms on its
// first winning tick (prompt) and then every reapEveryTicks (throttled), always
// exempting its own live survivor PID.
func TestExecutorWinnerReapsForeign(t *testing.T) {
	e, st, _, p := newExec(t) // lock wins by default
	if err := st.WriteDesired("v1"); err != nil {
		t.Fatal(err)
	}
	p.pid = 4242 // the survivor PID the reap must exempt
	var keepPIDs []int
	e.ReapForeign = func(keepPID int) (int, error) {
		keepPIDs = append(keepPIDs, keepPID)
		return 0, nil
	}
	ctx := context.Background()

	// Tick 1: acquire lock + start platform + reap (first winning tick).
	if _, err := e.Tick(ctx); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if len(keepPIDs) != 1 || keepPIDs[0] != 4242 {
		t.Fatalf("winner must reap on first tick exempting survivor PID, got %v", keepPIDs)
	}
	// Ticks 2..5: throttled — no further reap.
	for i := 0; i < 4; i++ {
		if _, err := e.Tick(ctx); err != nil {
			t.Fatalf("throttle tick: %v", err)
		}
	}
	if len(keepPIDs) != 1 {
		t.Fatalf("reap must be throttled within reapEveryTicks, got %d calls", len(keepPIDs))
	}
	// Tick 6: reaps again.
	if _, err := e.Tick(ctx); err != nil {
		t.Fatalf("tick 6: %v", err)
	}
	if len(keepPIDs) != 2 {
		t.Fatalf("reap must fire again after reapEveryTicks, got %d calls", len(keepPIDs))
	}
}

// TestExecutorLoserNeverReaps: a standby (lock loser) yields and NEVER reaps, so
// two daemons never fight over the process table.
func TestExecutorLoserNeverReaps(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	if err := st.WriteDesired("v1"); err != nil {
		t.Fatal(err)
	}
	p := &fakePlat{pid: 4242}
	e := NewExecutor(st, &fakeFetch{}, p, &fakeLock{acquireOK: false}, nil)
	called := 0
	e.ReapForeign = func(int) (int, error) { called++; return 0, nil }
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if _, err := e.Tick(ctx); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	if called != 0 {
		t.Fatalf("lock loser must never reap, got %d calls", called)
	}
}

// TestExecutorSkipsReapWithoutSurvivor: without a live survivor PID to exempt,
// the winner does NOT reap — it can never risk reaching zero platforms.
func TestExecutorSkipsReapWithoutSurvivor(t *testing.T) {
	e, st, _, p := newExec(t)
	if err := st.WriteDesired("v1"); err != nil {
		t.Fatal(err)
	}
	p.pid = 0 // no live platform to exempt
	called := 0
	e.ReapForeign = func(int) (int, error) { called++; return 0, nil }
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		if _, err := e.Tick(ctx); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	if called != 0 {
		t.Fatalf("reap must be skipped when survivor PID is unknown, got %d calls", called)
	}
}
