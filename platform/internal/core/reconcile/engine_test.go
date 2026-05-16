package reconcile

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/platform/internal/core/state"
)

// --- fakes ---

type fakePolicy struct {
	d   Desired
	err error
}

func (f *fakePolicy) Desired() (Desired, error) { return f.d, f.err }

type fakeObs struct {
	o   Observed
	err error
}

func (f *fakeObs) Observe() (Observed, error) { return f.o, f.err }

type fakeLease struct {
	acquireResult bool
	acquireErr    error
	acquires      int
}

func (f *fakeLease) Acquire(time.Duration) (bool, error) {
	f.acquires++
	return f.acquireResult, f.acquireErr
}
func (f *fakeLease) Release() error { return nil }

type fakeAct struct {
	enforce, respawn, repair int
	exitTarget               string
	alerts                   []string
	enforceErr               error
	stopOnExit               bool
}

func (a *fakeAct) EnforcePolicy() error  { a.enforce++; return a.enforceErr }
func (a *fakeAct) RespawnPartner() error { a.respawn++; return nil }
func (a *fakeAct) RepairLaunch() error   { a.repair++; return nil }
func (a *fakeAct) Alert(n string)        { a.alerts = append(a.alerts, n) }
func (a *fakeAct) ExitForUpgrade(t string) error {
	a.exitTarget = t
	if a.stopOnExit {
		return ErrStopLoop
	}
	return nil
}

func eng(p PolicySource, o Observer, l LeaseStore, a Actuator) *Engine {
	return &Engine{Policy: p, Obs: o, Lease: l, Act: a, IsBad: never}
}

func TestRunOnceSteadyNoSideEffects(t *testing.T) {
	a := &fakeAct{}
	e := eng(&fakePolicy{d: Desired{Version: "v1"}}, &fakeObs{o: okWorld()}, &fakeLease{}, a)
	dec, err := e.RunOnce(context.Background())
	if err != nil || !dec.Steady {
		t.Fatalf("steady expected: dec=%+v err=%v", dec, err)
	}
	if a.enforce+a.respawn+a.repair != 0 || a.exitTarget != "" {
		t.Errorf("no side effects expected, got %+v", a)
	}
}

func TestRunOnceEnforces(t *testing.T) {
	o := okWorld()
	o.PolicyApplied = false
	a := &fakeAct{}
	e := eng(&fakePolicy{d: Desired{Version: "v1"}}, &fakeObs{o: o}, &fakeLease{}, a)
	if _, err := e.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.enforce != 1 {
		t.Errorf("EnforcePolicy calls = %d, want 1", a.enforce)
	}
}

func TestRunOnceTier0FailSoft(t *testing.T) {
	o := okWorld()
	o.PolicyApplied = false
	o.PartnerAlive = false // also needs respawn
	a := &fakeAct{enforceErr: errors.New("disk full")}
	e := eng(&fakePolicy{d: Desired{Version: "v1"}}, &fakeObs{o: o}, &fakeLease{}, a)
	dec, err := e.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("a failing Tier-0 step must not fail the tick: %v", err)
	}
	// Enforce failed but RespawnPartner must STILL have been attempted.
	if a.enforce != 1 || a.respawn != 1 {
		t.Errorf("fail-soft broken: %+v dec=%+v", a, dec)
	}
}

func TestRunOncePolicyErrorKeepsDurableState(t *testing.T) {
	a := &fakeAct{}
	e := eng(&fakePolicy{err: errors.New("cache miss")}, &fakeObs{o: okWorld()}, &fakeLease{}, a)
	dec, err := e.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("policy error must not crash the tick: %v", err)
	}
	if a.enforce != 0 || dec.Note == "" {
		t.Errorf("policy error: expected no actuation, a note; got %+v %+v", a, dec)
	}
}

func TestRunOnceObserveErrorSkips(t *testing.T) {
	a := &fakeAct{}
	e := eng(&fakePolicy{d: Desired{Version: "v1"}}, &fakeObs{err: errors.New("ps failed")}, &fakeLease{}, a)
	if _, err := e.RunOnce(context.Background()); err != nil {
		t.Fatalf("observe error must not crash: %v", err)
	}
	if a.enforce != 0 {
		t.Error("must not actuate on observe failure")
	}
}

func TestRunOnceCanaryExitsForUpgrade(t *testing.T) {
	a := &fakeAct{stopOnExit: true}
	e := eng(&fakePolicy{d: Desired{Version: "v2"}}, &fakeObs{o: okWorld()},
		&fakeLease{acquireResult: true}, a)
	_, err := e.RunOnce(context.Background())
	if !errors.Is(err, ErrStopLoop) {
		t.Fatalf("canary should stop loop for upgrade, got %v", err)
	}
	if a.exitTarget != "v2" {
		t.Errorf("exit target = %q, want v2", a.exitTarget)
	}
}

func TestRunOnceLeaseAcquiredOnlyWhenUpgradePending(t *testing.T) {
	// Steady tick must NOT touch the lease.
	fl := &fakeLease{acquireResult: true}
	e := eng(&fakePolicy{d: Desired{Version: "v1"}}, &fakeObs{o: okWorld()}, fl, &fakeAct{})
	e.RunOnce(context.Background())
	if fl.acquires != 0 {
		t.Errorf("steady tick acquired lease %d times, want 0", fl.acquires)
	}

	// New desired version → lease IS attempted.
	fl2 := &fakeLease{acquireResult: false}
	e2 := eng(&fakePolicy{d: Desired{Version: "v2"}}, &fakeObs{o: okWorld()}, fl2, &fakeAct{})
	e2.RunOnce(context.Background())
	if fl2.acquires != 1 {
		t.Errorf("pending upgrade acquired lease %d times, want 1", fl2.acquires)
	}
}

func TestRunReturnsNilOnUpgradeExit(t *testing.T) {
	a := &fakeAct{stopOnExit: true}
	e := eng(&fakePolicy{d: Desired{Version: "v2"}}, &fakeObs{o: okWorld()},
		&fakeLease{acquireResult: true}, a)
	if err := e.Run(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("Run should return nil when loop stops for upgrade, got %v", err)
	}
}

func TestRunCancels(t *testing.T) {
	e := eng(&fakePolicy{d: Desired{Version: "v1"}}, &fakeObs{o: okWorld()}, &fakeLease{}, &fakeAct{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := e.Run(ctx, time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run should stop on context cancel, got %v", err)
	}
}

// --- DBLease integration (real SQLite job_locks) ---

func TestDBLeaseMutualExclusionAndExpiry(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	A := NewDBLease(db) // worker A
	B := NewDBLease(db) // worker B (same row/key)

	okA, _ := A.Acquire(time.Minute)
	if !okA {
		t.Fatal("A should acquire the free lease")
	}
	okB, _ := B.Acquire(time.Minute)
	if okB {
		t.Fatal("B must NOT acquire while A holds it (staggered upgrade)")
	}
	if err := A.Release(); err != nil {
		t.Fatal(err)
	}
	if okB2, _ := B.Acquire(time.Minute); !okB2 {
		t.Fatal("B should acquire after A releases")
	}
	if err := B.Release(); err != nil {
		t.Fatal(err)
	}

	// Expiry: a crashed canary (never releases) must not wedge upgrades.
	C := NewDBLease(db)
	if ok, _ := C.Acquire(time.Nanosecond); !ok {
		t.Fatal("C should acquire the free lease")
	}
	time.Sleep(2 * time.Millisecond) // C's lease expires; C never releases
	if ok, _ := A.Acquire(time.Minute); !ok {
		t.Error("expired lease must be reclaimable")
	}
}

// --- deterministic two-worker upgrade timeline (the §5 design) ---

func TestUpgradeTimelineStaggered(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	desired := Desired{Version: "v2"}
	good := "v1"
	obsFor := func(my string) Observer {
		return &fakeObs{o: Observed{
			MyVersion: my, GoodVersion: good,
			PolicyApplied: true, PartnerAlive: true, LaunchEntriesOK: true,
		}}
	}
	mkEngine := func(my string, a *fakeAct) *Engine {
		return &Engine{
			Policy: &fakePolicy{d: desired}, Obs: obsFor(my),
			Lease: NewDBLease(db), Act: a, IsBad: never,
			LeaseTTL: time.Minute,
		}
	}

	// 1. A ticks first: becomes canary, exits to v2.
	aAct := &fakeAct{stopOnExit: true}
	if _, err := mkEngine("v1", aAct).RunOnce(context.Background()); !errors.Is(err, ErrStopLoop) {
		t.Fatalf("A should exit for upgrade, err=%v", err)
	}
	if aAct.exitTarget != "v2" {
		t.Fatalf("A exit target=%q want v2", aAct.exitTarget)
	}

	// 2. B ticks while A holds the lease (good still v1): safety net —
	//    must NOT upgrade, must keep enforcing (steady), no exit.
	bAct := &fakeAct{stopOnExit: true}
	decB, err := mkEngine("v1", bAct).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("B tick errored: %v", err)
	}
	if bAct.exitTarget != "" || decB.Has(ActExitForUpgrade) {
		t.Fatalf("B must stay on v1 while A is canary: %+v", decB)
	}

	// 3. Canary proved healthy → promote good to v2, release lease.
	good = "v2"
	NewDBLease(db).Release()

	// 4. B ticks again: my=v1, good=v2 → catch-up exit to v2.
	bAct2 := &fakeAct{stopOnExit: true}
	if _, err := mkEngine("v1", bAct2).RunOnce(context.Background()); !errors.Is(err, ErrStopLoop) {
		t.Fatalf("B should catch up to good, err=%v", err)
	}
	if bAct2.exitTarget != "v2" {
		t.Fatalf("B catch-up target=%q want v2", bAct2.exitTarget)
	}

	// 5. Both on v2 now: steady, no churn.
	steady := &fakeAct{}
	dec, _ := mkEngine("v2", steady).RunOnce(context.Background())
	if !dec.Steady {
		t.Fatalf("post-upgrade should be steady: %+v", dec)
	}
}
