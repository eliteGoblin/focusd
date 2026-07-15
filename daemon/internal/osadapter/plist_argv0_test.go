package osadapter

import (
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// TestPlistProdSplitsProgramFromArgv0 (FEATURE 26, layer a): a non-test mesh plist
// emits Program=<real binary path> and ProgramArguments[0]=<spoof token>, so `ps
// aux` shows the token, not the disguised binary path — while launchd still execs
// the real Program. Plist() is pure string generation, so this is cross-platform.
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
// undisturbed. Plist() is pure string generation, so this is cross-platform.
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
