package osadapter

import (
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

func TestLabels(t *testing.T) {
	// Dev fallback (no Roster, not test): non-disguised default labels.
	s := Spec{}
	if s.Label(RoleA) != "com.focusd.daemon.a" ||
		s.Label(RoleB) != "com.focusd.daemon.b" ||
		s.Label(RoleEnsure) != "com.focusd.daemon.ensure" {
		t.Fatalf("dev labels wrong: %v %v %v",
			s.Label(RoleA), s.Label(RoleB), s.Label(RoleEnsure))
	}
	// Test mode: fixed e2e labels regardless of Roster (deterministic +
	// safely removable).
	ts := Spec{Mode: mode.Test}
	if ts.Label(RoleA) != "com.focusd.daemon.e2e.a" {
		t.Fatalf("test label wrong: %s", ts.Label(RoleA))
	}
	if ts.Label(RoleEnsure) != "com.focusd.daemon.e2e.ensure" {
		t.Fatalf("test ensure label wrong: %s", ts.Label(RoleEnsure))
	}
	if LabelFor(true, RoleEnsure) != "com.focusd.daemon.e2e.ensure" {
		t.Fatalf("LabelFor wrong: %s", LabelFor(true, RoleEnsure))
	}
	if len(AllRoles) != 3 {
		t.Fatalf("AllRoles must be 3, got %v", AllRoles)
	}
}

// TestLabelFromRoster asserts the FEATURE 10 scheme: a user/system Spec
// carries three INDEPENDENT labels in Roster, and Label indexes it by the
// role's position in AllRoles — no shared base, no role suffix appended.
func TestLabelFromRoster(t *testing.T) {
	roster := []string{
		"com.apple.metadata.helper.7f3a2c11ab",
		"com.google.keystone.daemon.8c1f4e9d22",
		"org.mozilla.updater.agent.0a1b2c3d4e",
	}
	s := Spec{Mode: mode.User, Roster: roster}
	if got := s.Label(RoleA); got != roster[0] {
		t.Errorf("RoleA label = %q, want %q", got, roster[0])
	}
	if got := s.Label(RoleB); got != roster[1] {
		t.Errorf("RoleB label = %q, want %q", got, roster[1])
	}
	if got := s.Label(RoleEnsure); got != roster[2] {
		t.Errorf("RoleEnsure label = %q, want %q", got, roster[2])
	}
	// The labels must NOT have a role token appended (they are used verbatim).
	if got := s.Label(RoleA); got != roster[0] {
		t.Errorf("Label must use roster entry verbatim, got %q", got)
	}
}

// TestLabelTestModeOverridesRoster asserts that even when a Roster is set,
// test mode short-circuits to the fixed e2e labels.
func TestLabelTestModeOverridesRoster(t *testing.T) {
	s := Spec{Mode: mode.Test, Roster: []string{"a.b.c", "d.e.f", "g.h.i"}}
	if got := s.Label(RoleA); got != "com.focusd.daemon.e2e.a" {
		t.Fatalf("test mode must override roster, got %q", got)
	}
}
