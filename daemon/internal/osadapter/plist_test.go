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
	// FEATURE 19: the PROD worker carries NO run/--mesh in argv — the marker
	// rides in EnvironmentVariables (MeshEnvKey="run:a"), hidden from `ps`.
	if strings.Contains(a, "<string>run</string>") || strings.Contains(a, "<string>--mesh</string>") {
		t.Fatalf("worker argv must NOT carry run/--mesh (FEATURE 19):\n%s", a)
	}
	if !strings.Contains(a, "<key>EnvironmentVariables</key><dict>") ||
		!strings.Contains(a, "<key>"+MeshEnvKey+"</key><string>run:a</string>") {
		t.Fatalf("worker must carry the mesh marker in env (MeshEnvKey=run:a):\n%s", a)
	}
	if strings.Contains(a, "StartInterval") {
		t.Fatal("worker must NOT have StartInterval")
	}

	e := Plist(s, RoleEnsure)
	// FEATURE 19: the ensurer subcommand also moves into env (MeshEnvKey=ensure);
	// argv is the binary alone. Scope the argv check to ProgramArguments — the
	// env dict legitimately holds the value "ensure".
	if strings.Contains(programArgsBlock(t, e), "<string>ensure</string>") {
		t.Fatalf("ensurer argv must NOT carry the ensure subcommand (FEATURE 19):\n%s", e)
	}
	if !strings.Contains(e, "<key>"+MeshEnvKey+"</key><string>ensure</string>") {
		t.Fatalf("ensurer must carry the ensure marker in env:\n%s", e)
	}
	if !strings.Contains(e, "<key>StartInterval</key><integer>30</integer>") {
		t.Fatalf("ensurer StartInterval wrong:\n%s", e)
	}
	if strings.Contains(e, "KeepAlive") {
		t.Fatal("ensurer must NOT have KeepAlive")
	}
}

// TestPlistProdArgvEmptyMarkerInEnv asserts FEATURE 19 / ADR-0018: a PROD mesh
// plist's argv is now EMPTY (the binary alone) — even the role/mesh marker
// FEATURE 14 had kept (`run --r <role> --mesh` / `ensure`) is gone from the
// command line. The marker rides in EnvironmentVariables (MeshEnvKey), and NONE
// of the disguised identifiers (the roster labels, --github/--asset/--interval/
// --workdir/--test-mode-flag) appear in argv. So `ps aux | grep mesh` finds
// nothing for the live mesh.
func TestPlistProdArgvEmptyMarkerInEnv(t *testing.T) {
	roster := []string{
		"com.apple.metadata.helper.7f3a2c11ab",
		"com.google.keystone.daemon.8c1f4e9d22",
		"org.mozilla.updater.agent.0a1b2c3d4e",
	}
	s := Spec{Mode: mode.User, SelfPath: "/d/daemon", Workdir: "/wd",
		Github: "o/r", Asset: "platform-darwin-arm64",
		Interval: 10 * time.Second, Roster: roster}

	// PROD argv (the strings after the binary) must be EMPTY for every role —
	// no run/--mesh/ensure marker, no disguised identifiers at all.
	for _, r := range AllRoles {
		if got := args(s, r); len(got) != 0 {
			t.Errorf("%s prod argv must be empty (FEATURE 19), got %v", r, got)
		}
	}

	// The marker moved into env: workers → run:<role>, ensurer → ensure.
	wantEnv := map[Role]string{RoleA: "run:a", RoleB: "run:b", RoleEnsure: "ensure"}
	for r, want := range wantEnv {
		kvs := env(s, r)
		if len(kvs) != 1 || kvs[0].Key != MeshEnvKey || kvs[0].Value != want {
			t.Errorf("%s env = %v, want [{%s %s}]", r, kvs, MeshEnvKey, want)
		}
	}

	// And `ps` (the ProgramArguments array — NOT the env dict, which ps does
	// not display) shows no mesh tell. Scope the scan to the ProgramArguments
	// block: the env VALUE "ensure"/"run:a" legitimately lives in the env dict.
	for _, r := range AllRoles {
		pa := programArgsBlock(t, Plist(s, r))
		for _, tok := range []string{"--mesh", "<string>run</string>", "<string>ensure</string>"} {
			if strings.Contains(pa, tok) {
				t.Errorf("%s ProgramArguments leaks %q (FEATURE 19):\n%s", r, tok, pa)
			}
		}
	}
}

// programArgsBlock returns the <key>ProgramArguments</key><array>…</array>
// substring of a rendered plist — what `ps` would surface — so a test can scan
// argv without matching the EnvironmentVariables dict.
func programArgsBlock(t *testing.T, plist string) string {
	t.Helper()
	const head = "<key>ProgramArguments</key><array>"
	i := strings.Index(plist, head)
	if i < 0 {
		t.Fatalf("plist has no ProgramArguments:\n%s", plist)
	}
	rest := plist[i:]
	end := strings.Index(rest, "</array>")
	if end < 0 {
		t.Fatalf("plist ProgramArguments not closed:\n%s", plist)
	}
	return rest[:end]
}

// TestArgvFromEnvRoundTrip asserts the CRITICAL invariant: the env value env()
// bakes for each role decodes EXACTLY back to the legacy subcommand argv the
// daemon's dispatch expects. A mismatch would mean a prod launchd start (which
// reconstructs argv from env) mis-dispatches or falls through to usage() and
// crash-loops under KeepAlive.
func TestArgvFromEnvRoundTrip(t *testing.T) {
	s := Spec{Mode: mode.User, SelfPath: "/d/daemon", Workdir: "/wd", Roster: []string{"x", "y", "z"}}
	want := map[Role][]string{
		RoleA:      {"run", "--r", "a", "--mesh"},
		RoleB:      {"run", "--r", "b", "--mesh"},
		RoleEnsure: {"ensure"},
	}
	for _, r := range AllRoles {
		kvs := env(s, r)
		if len(kvs) != 1 {
			t.Fatalf("%s: want exactly one env entry, got %v", r, kvs)
		}
		got := decodeMeshEnv(kvs[0].Value)
		if !equalArgs(got, want[r]) {
			t.Errorf("%s round-trip: decodeMeshEnv(%q) = %v, want %v", r, kvs[0].Value, got, want[r])
		}
	}
}

// TestDecodeMeshEnvSafeOnGarbage asserts a bad/missing env value yields NO
// synthesized argv (nil) — never a partial argv that could mis-dispatch. The
// caller then falls through to usage() rather than respawning into a wrong
// subcommand.
func TestDecodeMeshEnvSafeOnGarbage(t *testing.T) {
	// Strict inverse of encodeRole: only "ensure" / "run:a" / "run:b" decode.
	// Any unknown role ("run:a:b", "run:zzz") yields nil so a bad value can
	// never synthesize a partial argv that mis-dispatches into a crash-loop.
	for _, v := range []string{"", "run:", "run", "ensur", "garbage", "RUN:A", ":a", "run:a:b", "run:zzz"} {
		if got := decodeMeshEnv(v); got != nil {
			t.Errorf("decodeMeshEnv(%q) = %v, want nil", v, got)
		}
	}
}

// TestArgvFromEnvReadsProcessEnv exercises the os.Getenv path used by the prod
// entrypoint when launchd starts the binary with an empty argv.
func TestArgvFromEnvReadsProcessEnv(t *testing.T) {
	t.Setenv(MeshEnvKey, "run:b")
	if got := ArgvFromEnv(); !equalArgs(got, []string{"run", "--r", "b", "--mesh"}) {
		t.Fatalf("ArgvFromEnv = %v, want [run --r b --mesh]", got)
	}
	t.Setenv(MeshEnvKey, "")
	if got := ArgvFromEnv(); got != nil {
		t.Fatalf("ArgvFromEnv with empty var = %v, want nil", got)
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
