package main

import (
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// FEATURE 08 / ADR-0010: a failed SYSTEM install must fail fast with an
// explicit "re-run without sudo for the degraded user install" hint and
// MUST NOT silently fall back. A failed user/test install gets no hint
// (there is nothing lower to downgrade to).
func TestInstallFailureHint(t *testing.T) {
	sys := installFailureHint(mode.System, "v1.2.3")
	if len(sys) == 0 {
		t.Fatal("system-install failure must produce an explicit hint")
	}
	joined := strings.Join(sys, "\n")
	if !strings.Contains(joined, "NOT falling back") {
		t.Errorf("hint must state no auto-fallback, got:\n%s", joined)
	}
	if !strings.Contains(joined, "WITHOUT sudo") {
		t.Errorf("hint must tell operator to re-run without sudo, got:\n%s", joined)
	}
	if !strings.Contains(joined, "v1.2.3") {
		t.Errorf("hint must echo the desired version, got:\n%s", joined)
	}

	if installFailureHint(mode.User, "v1.2.3") != nil {
		t.Error("user-install failure must NOT print a downgrade hint")
	}
	if installFailureHint(mode.Test, "v1.2.3") != nil {
		t.Error("test-install failure must NOT print a downgrade hint")
	}
}

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
