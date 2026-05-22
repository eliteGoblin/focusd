package core

import "testing"

func TestDecideNoConfig(t *testing.T) {
	if got := Decide(State{}); got.Kind != ResolveLatest {
		t.Fatalf("no config ⇒ ResolveLatest, got %+v", got)
	}
	// Config flag set but Desired empty is still unresolved.
	if got := Decide(State{HaveConfig: true}); got.Kind != ResolveLatest {
		t.Fatalf("empty desired ⇒ ResolveLatest, got %+v", got)
	}
}

func TestDecideNothingRunning(t *testing.T) {
	got := Decide(State{HaveConfig: true, Desired: "v1"})
	if got.Kind != EnsureRunning || got.Target != "v1" {
		t.Fatalf("nothing running ⇒ EnsureRunning v1, got %+v", got)
	}
}

func TestDecideSteady(t *testing.T) {
	got := Decide(State{HaveConfig: true, Desired: "v1", Running: "v1"})
	if got.Kind != Steady {
		t.Fatalf("running == desired ⇒ Steady, got %+v", got)
	}
}

func TestDecideSwitchVersion(t *testing.T) {
	got := Decide(State{HaveConfig: true, Desired: "v2", Running: "v1"})
	if got.Kind != EnsureRunning || got.Target != "v2" {
		t.Fatalf("running v1, desired v2 ⇒ EnsureRunning v2, got %+v", got)
	}
}

func TestDecideRollbackWhenDesiredBad(t *testing.T) {
	s := State{HaveConfig: true, Desired: "v2", Running: "v2", Good: "v1",
		Bad: map[string]bool{"v2": true}}
	got := Decide(s)
	if got.Kind != Rollback || got.Target != "v1" {
		t.Fatalf("desired bad ⇒ Rollback to good v1, got %+v", got)
	}
}

func TestDecideAlreadyOnGoodAfterBadDesired(t *testing.T) {
	s := State{HaveConfig: true, Desired: "v2", Running: "v1", Good: "v1",
		Bad: map[string]bool{"v2": true}}
	if got := Decide(s); got.Kind != Steady || got.Target != "v1" {
		t.Fatalf("already on good ⇒ Steady v1, got %+v", got)
	}
}

func TestDecideBlockedNoGoodFallback(t *testing.T) {
	s := State{HaveConfig: true, Desired: "v2", Running: "v2",
		Bad: map[string]bool{"v2": true}} // no Good
	if got := Decide(s); got.Kind != Blocked {
		t.Fatalf("bad desired + no good ⇒ Blocked, got %+v", got)
	}
	// Good is itself bad ⇒ still blocked.
	s2 := State{HaveConfig: true, Desired: "v2", Good: "v1",
		Bad: map[string]bool{"v2": true, "v1": true}}
	if got := Decide(s2); got.Kind != Blocked {
		t.Fatalf("bad desired + bad good ⇒ Blocked, got %+v", got)
	}
}

func TestDecideBadDesiredNothingRunningRollsToGood(t *testing.T) {
	s := State{HaveConfig: true, Desired: "v2", Running: "", Good: "v1",
		Bad: map[string]bool{"v2": true}}
	got := Decide(s)
	if got.Kind != Rollback || got.Target != "v1" {
		t.Fatalf("bad desired, nothing running ⇒ Rollback v1, got %+v", got)
	}
}

func TestDecideIsTotalAndDeterministic(t *testing.T) {
	// Same input ⇒ same output, always a known Kind.
	s := State{HaveConfig: true, Desired: "v3", Running: "v2", Good: "v1"}
	a, b := Decide(s), Decide(s)
	if a != b {
		t.Fatalf("non-deterministic: %+v vs %+v", a, b)
	}
	switch a.Kind {
	case ResolveLatest, EnsureRunning, Rollback, Steady, Blocked:
	default:
		t.Fatalf("unknown kind %q", a.Kind)
	}
}
