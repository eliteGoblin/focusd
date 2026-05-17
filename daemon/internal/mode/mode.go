// Package mode is the SINGLE place that decides which install mode the
// daemon runs in and where each mode lives on disk.
//
// Release builds expose only two modes, chosen by how the daemon is run:
//
//	run normally  → User    → ~/Library/...           LaunchAgent  (gui/<uid>)
//	sudo run      → System  → /Library/...             LaunchDaemon (system)
//
// Test is a throwaway install used only by the e2e harness (fixed labels,
// caller-supplied workdir). It is never returned by Resolve and the CLI
// only exposes it under the `e2e` build tag, so a release binary cannot
// enter it (see cmd/daemon/testmode_*.go).
//
// Each mode has its own folder root, so a test install and a real install
// can never share a directory (the collision this package exists to kill).
package mode

import (
	"os"
	"path/filepath"
	"strconv"
)

// Mode is one install mode.
type Mode string

const (
	User   Mode = "user"   // unprivileged: ~/Library, LaunchAgent
	System Mode = "system" // root (sudo): /Library, LaunchDaemon
	Test   Mode = "test"   // e2e only: caller workdir, fixed labels
)

// Resolve picks the deployment mode from how the daemon was launched:
// effective-root (sudo) → System, otherwise → User. Test is never
// returned here — it is opt-in via the e2e build-tag CLI seam.
func Resolve() Mode { return resolveFor(os.Geteuid()) }

// resolveFor is the pure decision (euid → mode), split out so both
// branches are unit-testable without actually being root.
func resolveFor(euid int) Mode {
	if euid == 0 {
		return System
	}
	return User
}

// SupportRoot is the "Application Support" base the hidden workdir is
// created under for this mode. (Test does not relocate — the harness
// passes an explicit workdir — so it has no support root.)
func SupportRoot(m Mode, home string) string {
	if m == System {
		return "/Library/Application Support"
	}
	return filepath.Join(home, "Library", "Application Support")
}

// LaunchDir is the directory the mesh plists are written to. Only System
// differs (root LaunchDaemons); User and Test both use the user's
// LaunchAgents (the test harness runs as a normal user).
func LaunchDir(m Mode, home string) string {
	if m == System {
		return "/Library/LaunchDaemons"
	}
	return filepath.Join(home, "Library", "LaunchAgents")
}

// LaunchDomain is the launchctl domain target for this mode.
func LaunchDomain(m Mode, uid int) string {
	if m == System {
		return "system"
	}
	return "gui/" + strconv.Itoa(uid)
}
