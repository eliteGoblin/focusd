//go:build darwin

// The plist Program/argv0 split and parsePlist read-back are launchd (darwin)
// concerns — parsePlist lives in ctl_darwin.go — so this behaviour test is
// darwin-only, matching the other *_darwin tests in this package.
package osadapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// TestPlistProdSplitsProgramFromArgv0 (FEATURE 26, layer a): a non-test mesh plist
// emits Program=<real binary path> and ProgramArguments[0]=<spoof token>, so `ps
// aux` shows the token, not the disguised binary path — while launchd still execs
// the real Program.
func TestPlistProdSplitsProgramFromArgv0(t *testing.T) {
	const self = "/Users/x/Library/Application Support/Numi/bundle.payload.archive.aa11bb22cc"
	roster := []string{
		"com.apple.coreservices.spotlight",
		"MicrosoftUpdateHelper",
		"trustlocationd",
	}
	s := Spec{Mode: mode.User, SelfPath: self, Workdir: "/wd", Roster: roster}

	for _, r := range AllRoles {
		p := Plist(s, r)
		// Program key carries the REAL binary path.
		if !strings.Contains(p, "<key>Program</key><string>"+self+"</string>") {
			t.Fatalf("%s: Program must be the real binary path:\n%s", r, p)
		}
		// The ProgramArguments block must NOT contain the real binary path (that is
		// the whole point — `ps aux` argv shows only the token).
		pa := programArgsBlock(t, p)
		if strings.Contains(pa, self) {
			t.Fatalf("%s: ProgramArguments must NOT leak the binary path:\n%s", r, pa)
		}
		// argv[0] must be the derived display token.
		token := daemonArgv0(s, r)
		if token == "" {
			t.Fatalf("%s: expected a non-empty display token", r)
		}
		if !strings.Contains(pa, "<string>"+token+"</string>") {
			t.Fatalf("%s: ProgramArguments[0] must be the token %q:\n%s", r, token, pa)
		}
	}
}

// TestPlistTestModeKeepsLegacyArgv: test mode has NO Program key and keeps
// ProgramArguments[0]=SelfPath (the e2e/legacy form), so existing e2e discovery is
// undisturbed.
func TestPlistTestModeKeepsLegacyArgv(t *testing.T) {
	const self = "/tmp/e2e/daemon"
	s := Spec{Mode: mode.Test, SelfPath: self, Workdir: "/tmp/e2e-wd"}
	for _, r := range AllRoles {
		p := Plist(s, r)
		if strings.Contains(p, "<key>Program</key>") {
			t.Fatalf("%s: test-mode plist must NOT split Program:\n%s", r, p)
		}
		pa := programArgsBlock(t, p)
		if !strings.Contains(pa, "<string>"+self+"</string>") {
			t.Fatalf("%s: test-mode ProgramArguments[0] must be SelfPath:\n%s", r, pa)
		}
	}
}

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
