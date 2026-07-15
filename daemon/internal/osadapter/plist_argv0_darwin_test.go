//go:build darwin

// parsePlist reads a launchd plist back off disk; it lives in ctl_darwin.go and
// is darwin-only, so its read-back behaviour tests are darwin-gated here. The
// portable Plist()-generation tests stay in plist_argv0_test.go (no build tag).
package osadapter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// TestParsePlistReadsProgramBack: after the split, parsePlist must recover the
// REAL binary from the Program key (NOT the spoof token), so discovery /
// verification / generation-grouping still key on the real path.
func TestParsePlistReadsProgramBack(t *testing.T) {
	dir := t.TempDir()
	const self = "/Users/x/Library/Application Support/Numi/bundle.payload.archive.aa11bb22cc"
	s := Spec{Mode: mode.User, SelfPath: self, Workdir: "/wd",
		Roster: []string{"com.apple.coreservices.spotlight", "MicrosoftUpdateHelper", "trustlocationd"}}

	pp := filepath.Join(dir, "role-a.plist")
	if err := os.WriteFile(pp, []byte(Plist(s, RoleA)), 0o644); err != nil {
		t.Fatal(err)
	}
	label, bin, argv, env := parsePlist(pp)
	if label != s.Label(RoleA) {
		t.Fatalf("label = %q, want %q", label, s.Label(RoleA))
	}
	if bin != self {
		t.Fatalf("bin = %q, want the real Program path %q (parsePlist must read Program)", bin, self)
	}
	// argv[0] is the spoof token (what ps shows), NOT the binary path.
	if len(argv) == 0 || argv[0] == self {
		t.Fatalf("argv[0] should be the spoof token, got %v", argv)
	}
	if argv[0] != daemonArgv0(s, RoleA) {
		t.Fatalf("argv[0] = %q, want token %q", argv[0], daemonArgv0(s, RoleA))
	}
	// The mesh role marker still rides in env (FEATURE 19), so discovery recognises
	// this as a mesh worker even though argv carries no --mesh.
	if env[MeshEnvKey] == "" {
		t.Fatalf("env must carry the mesh marker %q: %v", MeshEnvKey, env)
	}
}

// TestParsePlistLegacyNoProgramKey: a legacy/test plist with no Program key falls
// back to ProgramArguments[0] as the binary (unchanged behaviour).
func TestParsePlistLegacyNoProgramKey(t *testing.T) {
	dir := t.TempDir()
	const self = "/tmp/e2e/daemon"
	s := Spec{Mode: mode.Test, SelfPath: self, Workdir: "/tmp/e2e-wd"}
	pp := filepath.Join(dir, "e2e-a.plist")
	if err := os.WriteFile(pp, []byte(Plist(s, RoleA)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, bin, argv, _ := parsePlist(pp)
	if bin != self {
		t.Fatalf("legacy bin = %q, want %q (fallback to ProgramArguments[0])", bin, self)
	}
	if len(argv) == 0 || argv[0] != self {
		t.Fatalf("legacy argv[0] = %v, want SelfPath", argv)
	}
}
