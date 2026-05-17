package mode

import "testing"

func TestResolveForByEuid(t *testing.T) {
	if got := resolveFor(0); got != System {
		t.Fatalf("euid 0 → %q, want %q", got, System)
	}
	for _, euid := range []int{1, 501, 99999} {
		if got := resolveFor(euid); got != User {
			t.Fatalf("euid %d → %q, want %q", euid, got, User)
		}
	}
}

func TestResolveReturnsADeploymentMode(t *testing.T) {
	// Never Test (that is opt-in via the e2e CLI seam), and exactly one
	// of User/System depending on whether the test runs as root.
	switch got := Resolve(); got {
	case User, System:
	default:
		t.Fatalf("Resolve() = %q, want user or system", got)
	}
}

func TestPathsPerMode(t *testing.T) {
	const home = "/Users/alice"
	cases := []struct {
		m                              Mode
		wantSupport, wantLaunch, domNm string
		uid                            int
	}{
		{User, "/Users/alice/Library/Application Support", "/Users/alice/Library/LaunchAgents", "gui/501", 501},
		{System, "/Library/Application Support", "/Library/LaunchDaemons", "system", 501},
		{Test, "/Users/alice/Library/Application Support", "/Users/alice/Library/LaunchAgents", "gui/501", 501},
	}
	for _, c := range cases {
		if got := SupportRoot(c.m, home); got != c.wantSupport {
			t.Errorf("SupportRoot(%s) = %q, want %q", c.m, got, c.wantSupport)
		}
		if got := LaunchDir(c.m, home); got != c.wantLaunch {
			t.Errorf("LaunchDir(%s) = %q, want %q", c.m, got, c.wantLaunch)
		}
		if got := LaunchDomain(c.m, c.uid); got != c.domNm {
			t.Errorf("LaunchDomain(%s) = %q, want %q", c.m, got, c.domNm)
		}
	}
}

// The folder-collision invariant this package exists to guarantee:
// a user install and a system install can never share a directory.
func TestUserAndSystemNeverShareFolder(t *testing.T) {
	const home = "/Users/alice"
	if SupportRoot(User, home) == SupportRoot(System, home) {
		t.Fatal("user and system support roots must differ")
	}
	if LaunchDir(User, home) == LaunchDir(System, home) {
		t.Fatal("user and system launch dirs must differ")
	}
}
