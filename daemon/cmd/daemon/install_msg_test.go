package main

import (
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// A successful USER install is the deliberate degraded fallback: it must
// honestly report that the system-level protections are unavailable.
// System/Test installs print no such notice.
func TestInstallCoverageNotice(t *testing.T) {
	usr := installCoverageNotice(mode.User)
	if len(usr) == 0 {
		t.Fatal("user-mode install must print a coverage notice")
	}
	joined := strings.Join(usr, "\n")
	if !strings.Contains(joined, "UNAVAILABLE") {
		t.Errorf("notice must mark system protections unavailable, got:\n%s", joined)
	}
	if !strings.Contains(strings.ToLower(joined), "sudo") {
		t.Errorf("notice must point at the sudo (full) install, got:\n%s", joined)
	}

	if installCoverageNotice(mode.System) != nil {
		t.Error("system-mode install must NOT print the degraded notice")
	}
	if installCoverageNotice(mode.Test) != nil {
		t.Error("test-mode install must NOT print the degraded notice")
	}
}
