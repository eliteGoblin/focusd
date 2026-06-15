package osadapter

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

func TestPlistWorkerVsEnsurer(t *testing.T) {
	s := Spec{SelfPath: "/d/daemon", Workdir: "/wd", Github: "o/r",
		Asset: "platform-darwin-arm64", Interval: 2 * time.Second,
		EnsureInterval: 30 * time.Second}

	a := Plist(s, RoleA)
	if !strings.Contains(a, "<string>com.focusd.daemon.a</string>") {
		t.Fatal("A label missing")
	}
	if !strings.Contains(a, "<key>KeepAlive</key><true/>") {
		t.Fatal("worker must have KeepAlive")
	}
	if !strings.Contains(a, "<string>run</string>") || !strings.Contains(a, "<string>--mesh</string>") {
		t.Fatal("worker args must include run + --mesh")
	}
	if strings.Contains(a, "StartInterval") {
		t.Fatal("worker must NOT have StartInterval")
	}

	e := Plist(s, RoleEnsure)
	if !strings.Contains(e, "<string>ensure</string>") {
		t.Fatal("ensurer must run the ensure subcommand")
	}
	if !strings.Contains(e, "<key>StartInterval</key><integer>30</integer>") {
		t.Fatalf("ensurer StartInterval wrong:\n%s", e)
	}
	if strings.Contains(e, "KeepAlive") {
		t.Fatal("ensurer must NOT have KeepAlive")
	}
}

// TestPlistEmitsFullRoster asserts FEATURE 10: each plist's argv carries
// the FULL 3-label roster (not a --mesh-base), so any survivor relaunched
// from its own launch args reconstructs Spec.Roster. The roster value is
// the three labels joined — every plist is self-describing.
func TestPlistEmitsFullRoster(t *testing.T) {
	roster := []string{
		"com.apple.metadata.helper.7f3a2c11ab",
		"com.google.keystone.daemon.8c1f4e9d22",
		"org.mozilla.updater.agent.0a1b2c3d4e",
	}
	s := Spec{Mode: mode.User, SelfPath: "/d/daemon", Workdir: "/wd",
		Github: "o/r", Asset: "platform-darwin-arm64",
		Interval: 10 * time.Second, Roster: roster}

	for _, r := range AllRoles {
		p := Plist(s, r)
		if strings.Contains(p, "--mesh-base") {
			t.Errorf("%s plist must NOT carry the retired --mesh-base flag", r)
		}
		if !strings.Contains(p, "<string>--roster</string>") {
			t.Errorf("%s plist must carry --roster", r)
		}
		want := strings.Join(roster, ",")
		if !strings.Contains(p, "<string>"+want+"</string>") {
			t.Errorf("%s plist missing full roster %q:\n%s", r, want, p)
		}
	}
}

// TestRosterArgvRoundTrip asserts the survivor-reconstruct contract: the
// roster flag value parses back into the same three labels.
func TestRosterArgvRoundTrip(t *testing.T) {
	roster := []string{
		"com.apple.metadata.helper.7f3a2c11ab",
		"com.google.keystone.daemon.8c1f4e9d22",
		"org.mozilla.updater.agent.0a1b2c3d4e",
	}
	joined := strings.Join(roster, ",")
	got := strings.Split(joined, ",")
	if len(got) != 3 {
		t.Fatalf("round-trip len = %d", len(got))
	}
	for i := range roster {
		if got[i] != roster[i] {
			t.Errorf("round-trip[%d] = %q, want %q", i, got[i], roster[i])
		}
	}
}

func TestIntervalSecondsFloor(t *testing.T) {
	// Sub-second EnsureInterval still floors to 1.
	if got := intervalSeconds(Spec{EnsureInterval: 100 * time.Millisecond}); got != 1 {
		t.Fatalf("sub-second EnsureInterval must floor to 1, got %d", got)
	}
	if got := intervalSeconds(Spec{EnsureInterval: 5 * time.Second}); got != 5 {
		t.Fatalf("got %d", got)
	}
}

// TestEnsureIntervalDecoupledFromWorker asserts FEATURE 10: the ensurer
// StartInterval uses EnsureInterval (the ~10s backstop), NOT the fast
// worker Interval (~2s). A Spec with only a fast worker Interval set must
// still render the backstop default, not 2s.
func TestEnsureIntervalDecoupledFromWorker(t *testing.T) {
	s := Spec{SelfPath: "/d/daemon", Workdir: "/wd", Github: "o/r",
		Asset: "platform-darwin-arm64", Interval: 2 * time.Second}
	e := Plist(s, RoleEnsure)
	want := int(EnsureBackstopInterval.Seconds())
	if !strings.Contains(e, "<key>StartInterval</key><integer>"+strconv.Itoa(want)+"</integer>") {
		t.Fatalf("ensurer must use the %ds backstop, not the 2s worker cadence:\n%s", want, e)
	}
	// And the worker argv must still carry the fast 2s --interval.
	a := Plist(s, RoleA)
	if !strings.Contains(a, "<string>--interval</string>") || !strings.Contains(a, "<string>2s</string>") {
		t.Fatalf("worker --interval should be the fast 2s cadence:\n%s", a)
	}
}
