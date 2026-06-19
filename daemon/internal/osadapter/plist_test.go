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

// TestPlistProdArgvMinimized asserts FEATURE 14 / ADR-0018: a PROD mesh
// plist's argv is reduced to role + mesh marker and NONE of the disguised
// identifiers (the three roster labels — the bootout keys — plus --github,
// --asset, --interval, --workdir, --test-mode-flag). The masked workdir
// file, not argv, is the single source of truth for the labels.
func TestPlistProdArgvMinimized(t *testing.T) {
	roster := []string{
		"com.apple.metadata.helper.7f3a2c11ab",
		"com.google.keystone.daemon.8c1f4e9d22",
		"org.mozilla.updater.agent.0a1b2c3d4e",
	}
	s := Spec{Mode: mode.User, SelfPath: "/d/daemon", Workdir: "/wd",
		Github: "o/r", Asset: "platform-darwin-arm64",
		Interval: 10 * time.Second, Roster: roster}

	// Forbidden tokens: the leak FEATURE 14 closes. The three labels are
	// the worst (bootout keys); the rest each narrow the search. We scan the
	// ProgramArguments (what `ps` shows), NOT the plist Label key — the
	// launchd Label legitimately carries the disguised name on disk (that is
	// FEATURE 10's masked-roster concern, not this feature's argv concern).
	forbidden := []string{
		"--roster", "--github", "--asset", "--interval",
		"--workdir", "--test-mode-flag", "/wd", "o/r",
		"platform-darwin-arm64",
	}
	forbidden = append(forbidden, roster...)

	for _, r := range AllRoles {
		argv := args(s, r)
		for _, tok := range forbidden {
			for _, a := range argv {
				if a == tok {
					t.Errorf("%s prod argv leaks %q: %v", r, tok, argv)
				}
			}
		}
	}

	// Workers carry exactly: run --r <role> --mesh.
	for _, r := range []Role{RoleA, RoleB} {
		got := args(s, r)
		want := []string{"run", "--r", string(r), "--mesh"}
		if !equalArgs(got, want) {
			t.Errorf("%s worker argv = %v, want %v", r, got, want)
		}
	}
	// Ensurer carries exactly: ensure.
	if got := args(s, RoleEnsure); !equalArgs(got, []string{"ensure"}) {
		t.Errorf("ensurer argv = %v, want [ensure]", got)
	}
}

// TestPlistTestModeArgvCarriesWorkdir asserts the FEATURE 14 test-mode
// EXCEPTION: e2e installs still bake --test-mode-flag true + --workdir,
// because the throwaway e2e workdir is NOT derivable from argv[0] (the
// binary is not relocated inside it). No prod identifiers are baked.
func TestPlistTestModeArgvCarriesWorkdir(t *testing.T) {
	s := Spec{Mode: mode.Test, SelfPath: "/d/daemon", Workdir: "/tmp/e2e-wd",
		Github: "o/r", Asset: "platform-darwin-arm64", Interval: 2 * time.Second}

	worker := args(s, RoleA)
	wantWorker := []string{"run", "--r", "a", "--mesh", "--test-mode-flag", "true", "--workdir", "/tmp/e2e-wd"}
	if !equalArgs(worker, wantWorker) {
		t.Errorf("test-mode worker argv = %v, want %v", worker, wantWorker)
	}
	ensure := args(s, RoleEnsure)
	wantEnsure := []string{"ensure", "--test-mode-flag", "true", "--workdir", "/tmp/e2e-wd"}
	if !equalArgs(ensure, wantEnsure) {
		t.Errorf("test-mode ensurer argv = %v, want %v", ensure, wantEnsure)
	}
	// Still no --roster / --github / --asset / --interval even in test mode.
	for _, tok := range []string{"--roster", "--github", "--asset", "--interval"} {
		for _, a := range append(worker, ensure...) {
			if a == tok {
				t.Errorf("test-mode argv must not carry %q", tok)
			}
		}
	}
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
	// FEATURE 14 / ADR-0018: the worker no longer bakes --interval into argv
	// (the fast cadence is the daemon's built-in default; a baked value would
	// leak the cadence in `ps`). The ensurer's StartInterval — a launchd plist
	// key, NOT an argv flag — still carries the decoupled backstop above.
	a := Plist(s, RoleA)
	if strings.Contains(a, "<string>--interval</string>") {
		t.Fatalf("worker argv must NOT bake --interval (FEATURE 14):\n%s", a)
	}
}
