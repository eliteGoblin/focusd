package osadapter

import (
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

func TestLabels(t *testing.T) {
	s := Spec{}
	if s.Label(RoleA) != "com.focusd.daemon.a" ||
		s.Label(RoleB) != "com.focusd.daemon.b" ||
		s.Label(RoleEnsure) != "com.focusd.daemon.ensure" {
		t.Fatalf("prod labels wrong: %v %v %v",
			s.Label(RoleA), s.Label(RoleB), s.Label(RoleEnsure))
	}
	ts := Spec{Mode: mode.Test}
	if ts.Label(RoleA) != "com.focusd.daemon.e2e.a" {
		t.Fatalf("test label wrong: %s", ts.Label(RoleA))
	}
	if LabelFor(true, RoleEnsure) != "com.focusd.daemon.e2e.ensure" {
		t.Fatalf("LabelFor wrong: %s", LabelFor(true, RoleEnsure))
	}
	if len(AllRoles) != 3 {
		t.Fatalf("AllRoles must be 3, got %v", AllRoles)
	}
}
