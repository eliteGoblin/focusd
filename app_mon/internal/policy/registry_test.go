package policy

import (
	"context"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// fakePolicy is a minimal AppPolicy used to exercise Registry generics
// without coupling these tests to Steam/Dota2 specifics.
type fakePolicy struct {
	id       string
	name     string
	patterns []string
	paths    []string
	interval time.Duration
}

func (f *fakePolicy) ID() string                                                   { return f.id }
func (f *fakePolicy) Name() string                                                 { return f.name }
func (f *fakePolicy) ProcessPatterns() []string                                    { return f.patterns }
func (f *fakePolicy) PathsToDelete() []string                                      { return f.paths }
func (f *fakePolicy) ScanInterval() time.Duration                                  { return f.interval }
func (f *fakePolicy) PreEnforce(context.Context) error                             { return nil }
func (f *fakePolicy) PostEnforce(context.Context, *domain.EnforcementResult) error { return nil }

func TestNewRegistry_RegistersDefaultPolicies(t *testing.T) {
	// The default Registry IS the protection contract — if it doesn't
	// include Steam + Dota2, the daemon enforces nothing on production.
	r := NewRegistry()

	if _, ok := r.Get("steam"); !ok {
		t.Error("NewRegistry must include steam policy by default")
	}
	if _, ok := r.Get("dota2"); !ok {
		t.Error("NewRegistry must include dota2 policy by default")
	}

	ids := r.List()
	if len(ids) < 2 {
		t.Errorf("expected ≥2 default policies, got %d: %v", len(ids), ids)
	}
}

func TestNewRegistryWithPolicies_OverridesDefaults(t *testing.T) {
	a := &fakePolicy{id: "a", name: "A"}
	b := &fakePolicy{id: "b", name: "B"}

	r := NewRegistryWithPolicies(a, b)
	if got := len(r.List()); got != 2 {
		t.Fatalf("expected 2 policies, got %d", got)
	}
	if _, ok := r.Get("steam"); ok {
		t.Error("custom Registry should not include default steam policy")
	}
	if p, ok := r.Get("a"); !ok || p.ID() != "a" {
		t.Errorf("Get(\"a\") = %v, %v", p, ok)
	}
}

func TestRegistry_RegisterOverwritesByID(t *testing.T) {
	// Last-write-wins on collision. This matters for hot-swap during
	// updates: a fresh policy replaces the previous version with the
	// same ID, no leftover stale rules.
	r := NewRegistryWithPolicies()
	r.Register(&fakePolicy{id: "x", name: "first"})
	r.Register(&fakePolicy{id: "x", name: "second"})

	p, ok := r.Get("x")
	if !ok {
		t.Fatal("policy x missing after re-register")
	}
	if p.Name() != "second" {
		t.Errorf("expected last write to win; Name = %q, want %q", p.Name(), "second")
	}
	if got := len(r.List()); got != 1 {
		t.Errorf("expected 1 policy after re-register, got %d", got)
	}
}

func TestRegistry_GetUnknownReturnsFalse(t *testing.T) {
	r := NewRegistryWithPolicies()
	if p, ok := r.Get("nonexistent"); ok || p != nil {
		t.Errorf("Get(unknown) = %v, %v; want nil, false", p, ok)
	}
}

func TestRegistry_GetAllReturnsEveryPolicy(t *testing.T) {
	a := &fakePolicy{id: "a"}
	b := &fakePolicy{id: "b"}
	c := &fakePolicy{id: "c"}
	r := NewRegistryWithPolicies(a, b, c)

	all := r.GetAll()
	if len(all) != 3 {
		t.Fatalf("GetAll size = %d, want 3", len(all))
	}
	seen := map[string]struct{}{}
	for _, p := range all {
		seen[p.ID()] = struct{}{}
	}
	for _, want := range []string{"a", "b", "c"} {
		if _, ok := seen[want]; !ok {
			t.Errorf("GetAll missing %q", want)
		}
	}
}

func TestRegistryPolicyStore_RoundTripsViaToPolicy(t *testing.T) {
	// PolicyStore is what the enforcer consumes. It MUST surface
	// every registered policy as a domain.Policy with all fields
	// converted by ToPolicy — otherwise process patterns or paths
	// silently drop and protection breaks.
	store := NewPolicyStore()

	all := store.GetAll()
	if len(all) < 2 {
		t.Fatalf("expected ≥2 default domain.Policy entries, got %d", len(all))
	}
	for _, p := range all {
		if p.ID == "" {
			t.Errorf("ToPolicy produced empty ID for entry %+v", p)
		}
		if p.Name == "" {
			t.Errorf("ToPolicy produced empty Name for entry %+v", p)
		}
		if len(p.ProcessNames) == 0 {
			t.Errorf("policy %q has zero ProcessNames — would kill nothing", p.ID)
		}
		if len(p.Paths) == 0 {
			t.Errorf("policy %q has zero Paths — would delete nothing", p.ID)
		}
		if p.ScanInterval == 0 {
			t.Errorf("policy %q has zero ScanInterval — would never re-scan", p.ID)
		}
	}
}

func TestRegistryPolicyStore_GetByID(t *testing.T) {
	store := NewPolicyStore()

	p, err := store.GetByID("steam")
	if err != nil {
		t.Fatalf("GetByID(steam): %v", err)
	}
	if p == nil || p.ID != "steam" {
		t.Errorf("GetByID(steam) = %+v", p)
	}

	if _, err := store.GetByID("not-real"); err == nil {
		t.Error("GetByID(unknown) should error, got nil")
	}
}

func TestRegistryPolicyStore_List(t *testing.T) {
	store := NewPolicyStore()
	ids := store.List()
	if len(ids) < 2 {
		t.Errorf("List returned %d ids, want ≥2", len(ids))
	}
}

func TestToPolicy_CopiesAllFields(t *testing.T) {
	// Direct test for the conversion that PolicyStore.GetAll relies on.
	src := &fakePolicy{
		id:       "x",
		name:     "X App",
		patterns: []string{"x.bin", "X Helper"},
		paths:    []string{"/Applications/X.app", "/Users/u/.config/x"},
		interval: 7 * time.Minute,
	}
	got := ToPolicy(src)

	if got.ID != "x" || got.Name != "X App" {
		t.Errorf("ToPolicy ID/Name mismatch: %+v", got)
	}
	if got.ScanInterval != 7*time.Minute {
		t.Errorf("ToPolicy ScanInterval = %v, want %v", got.ScanInterval, 7*time.Minute)
	}
	if len(got.ProcessNames) != 2 || got.ProcessNames[0] != "x.bin" {
		t.Errorf("ToPolicy ProcessNames lost data: %v", got.ProcessNames)
	}
	if len(got.Paths) != 2 || got.Paths[1] != "/Users/u/.config/x" {
		t.Errorf("ToPolicy Paths lost data: %v", got.Paths)
	}
}
