package reconciler

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// --- fakes ---

type fakeResolver struct {
	// per-domain answers; nil err means a clean (possibly empty) answer
	answers map[string][]string
	errors  map[string]error
}

func (f *fakeResolver) ResolveA(_ context.Context, name string) ([]string, error) {
	if e, ok := f.errors[name]; ok {
		return nil, e
	}
	return f.answers[name], nil
}

type fakePf struct {
	current  []string
	addErr   error
	delErr   error
	showErr  error
	addCalls []string
	delCalls []string
}

func (f *fakePf) Show(_ context.Context) ([]string, error) {
	if f.showErr != nil {
		return nil, f.showErr
	}
	return append([]string{}, f.current...), nil
}

func (f *fakePf) Add(_ context.Context, ip string) error {
	f.addCalls = append(f.addCalls, ip)
	return f.addErr
}

func (f *fakePf) Delete(_ context.Context, ip string) error {
	f.delCalls = append(f.delCalls, ip)
	return f.delErr
}

// --- pure diff tests ---

func TestDiff_AddsAndRemovals(t *testing.T) {
	desired := toSet([]string{"1.1.1.1", "2.2.2.2", "3.3.3.3"})
	current := toSet([]string{"2.2.2.2", "4.4.4.4"})

	add, rem := diff(desired, current)
	wantAdd := []string{"1.1.1.1", "3.3.3.3"}
	wantRem := []string{"4.4.4.4"}
	if !reflect.DeepEqual(add, wantAdd) {
		t.Errorf("add = %v, want %v", add, wantAdd)
	}
	if !reflect.DeepEqual(rem, wantRem) {
		t.Errorf("remove = %v, want %v", rem, wantRem)
	}
}

func TestDiff_BothEmpty(t *testing.T) {
	add, rem := diff(map[string]struct{}{}, map[string]struct{}{})
	if len(add) != 0 || len(rem) != 0 {
		t.Errorf("both empty: add=%v rem=%v", add, rem)
	}
}

func TestDiff_IdenticalSets(t *testing.T) {
	s := toSet([]string{"1.1.1.1", "2.2.2.2"})
	add, rem := diff(s, s)
	if len(add) != 0 || len(rem) != 0 {
		t.Errorf("identical sets should have empty diff, got add=%v rem=%v", add, rem)
	}
}

func TestDiff_Deterministic(t *testing.T) {
	desired := toSet([]string{"9.9.9.9", "1.1.1.1", "5.5.5.5"})
	current := toSet([]string{"7.7.7.7", "3.3.3.3"})
	add, rem := diff(desired, current)
	if !sort.StringsAreSorted(add) {
		t.Errorf("add not sorted: %v", add)
	}
	if !sort.StringsAreSorted(rem) {
		t.Errorf("rem not sorted: %v", rem)
	}
}

// --- Reconcile happy path ---

func TestReconcile_HappyPath(t *testing.T) {
	res := &fakeResolver{
		answers: map[string][]string{
			"a.com": {"1.1.1.1", "2.2.2.2"},
			"b.com": {"3.3.3.3"},
		},
	}
	pf := &fakePf{current: []string{"2.2.2.2", "9.9.9.9"}}
	var buf bytes.Buffer
	r := &Reconciler{Resolver: res, Pf: pf, Domains: []string{"a.com", "b.com"}, Logger: &buf}

	out, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	wantAdd := []string{"1.1.1.1", "3.3.3.3"}
	wantRem := []string{"9.9.9.9"}
	if !reflect.DeepEqual(out.Added, wantAdd) {
		t.Errorf("added = %v, want %v", out.Added, wantAdd)
	}
	if !reflect.DeepEqual(out.Removed, wantRem) {
		t.Errorf("removed = %v, want %v", out.Removed, wantRem)
	}
	// CurrentCount is the post-apply size: started with 2, added 2, removed 1 = 3.
	if out.CurrentCount != 3 {
		t.Errorf("CurrentCount = %d, want 3", out.CurrentCount)
	}
	if !reflect.DeepEqual(pf.addCalls, wantAdd) {
		t.Errorf("pf.add calls = %v, want %v", pf.addCalls, wantAdd)
	}
	if !reflect.DeepEqual(pf.delCalls, wantRem) {
		t.Errorf("pf.delete calls = %v, want %v", pf.delCalls, wantRem)
	}
	// Logger should mention each operation for observability.
	for _, ip := range wantAdd {
		if !strings.Contains(buf.String(), "add "+ip) {
			t.Errorf("log missing add %s", ip)
		}
	}
}

func TestReconcile_NoChanges(t *testing.T) {
	res := &fakeResolver{answers: map[string][]string{"a.com": {"1.1.1.1"}}}
	pf := &fakePf{current: []string{"1.1.1.1"}}
	r := &Reconciler{Resolver: res, Pf: pf, Domains: []string{"a.com"}}

	out, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(out.Added) != 0 || len(out.Removed) != 0 {
		t.Errorf("expected no diff, got %+v", out)
	}
	if len(pf.addCalls) != 0 || len(pf.delCalls) != 0 {
		t.Error("no pfctl mutations should have been issued")
	}
}

// --- safety belt ---

func TestReconcile_AllDoHFails_RefusesWipe(t *testing.T) {
	res := &fakeResolver{
		errors: map[string]error{
			"a.com": errors.New("network down"),
			"b.com": errors.New("network down"),
		},
	}
	pf := &fakePf{current: []string{"1.1.1.1", "2.2.2.2"}}
	r := &Reconciler{Resolver: res, Pf: pf, Domains: []string{"a.com", "b.com"}}

	_, err := r.Reconcile(context.Background())
	if err == nil {
		t.Fatal("expected refusal-to-wipe error")
	}
	if len(pf.delCalls) != 0 {
		t.Errorf("MUST NOT delete anything; got %v", pf.delCalls)
	}
}

// Copilot review: partial DoH failure → fail closed.
// If domain "b.com" resolves cleanly but "a.com" fails transiently,
// the OLD behavior used b.com's incomplete answer set to compute
// removals — which could delete a.com's still-blocked IPs from the
// pf table, temporarily UNBLOCKING the hosts this job exists to
// block. Fix: any per-domain failure → reconciler returns an error;
// apply() never runs; next 30m tick retries. Brief table staleness
// is tolerable; silent unblock is not.
func TestReconcile_PartialDoHFailure_FailsClosed(t *testing.T) {
	res := &fakeResolver{
		answers: map[string][]string{"a.com": {"1.1.1.1"}},
		errors:  map[string]error{"b.com": errors.New("transient")},
	}
	pf := &fakePf{current: []string{"9.9.9.9"}} // pre-existing entry that would be wrongly deleted
	r := &Reconciler{Resolver: res, Pf: pf, Domains: []string{"a.com", "b.com"}}

	_, err := r.Reconcile(context.Background())
	if err == nil {
		t.Fatal("partial DoH failure MUST surface as a reconcile error (fail closed)")
	}
	if !strings.Contains(err.Error(), "resolve b.com") {
		t.Errorf("error should mention the failed domain: %v", err)
	}
	// CRITICAL: no pf mutations on a partial-failure tick.
	if len(pf.addCalls) != 0 {
		t.Errorf("MUST NOT add anything; got %v", pf.addCalls)
	}
	if len(pf.delCalls) != 0 {
		t.Errorf("MUST NOT delete anything; got %v", pf.delCalls)
	}
}

// --- pfctl failure modes ---

func TestReconcile_PfctlShowFails(t *testing.T) {
	res := &fakeResolver{answers: map[string][]string{"a.com": {"1.1.1.1"}}}
	pf := &fakePf{showErr: errors.New("not permitted")}
	r := &Reconciler{Resolver: res, Pf: pf, Domains: []string{"a.com"}}

	if _, err := r.Reconcile(context.Background()); err == nil {
		t.Fatal("expected error when pfctl show fails")
	}
}

func TestReconcile_PfctlAddFails_SurfacesError(t *testing.T) {
	res := &fakeResolver{answers: map[string][]string{"a.com": {"1.1.1.1"}}}
	pf := &fakePf{addErr: errors.New("operation not permitted")}
	r := &Reconciler{Resolver: res, Pf: pf, Domains: []string{"a.com"}}

	_, err := r.Reconcile(context.Background())
	if err == nil {
		t.Fatal("expected error when pfctl add fails")
	}
	if !strings.Contains(err.Error(), "not permitted") {
		t.Errorf("error %q should bubble up pfctl message", err)
	}
}

// --- input validation ---

func TestReconcile_MissingDeps(t *testing.T) {
	if _, err := (&Reconciler{}).Reconcile(context.Background()); err == nil {
		t.Error("missing resolver/pf should error")
	}
}

func TestReconcile_NoDomains(t *testing.T) {
	r := &Reconciler{Resolver: &fakeResolver{}, Pf: &fakePf{}}
	if _, err := r.Reconcile(context.Background()); err == nil {
		t.Error("empty domain list should error")
	}
}

// --- union dedup across domains ---

func TestReconcile_DedupsIPsAcrossDomains(t *testing.T) {
	res := &fakeResolver{answers: map[string][]string{
		"a.com": {"1.1.1.1", "2.2.2.2"},
		"b.com": {"1.1.1.1", "3.3.3.3"}, // 1.1.1.1 dup
	}}
	pf := &fakePf{}
	r := &Reconciler{Resolver: res, Pf: pf, Domains: []string{"a.com", "b.com"}}

	out, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}
	if !reflect.DeepEqual(out.Added, want) {
		t.Errorf("added = %v, want %v (deduped)", out.Added, want)
	}
}
