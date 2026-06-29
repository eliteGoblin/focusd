// Package companion holds the OS-agnostic layout + liveness logic for the
// out-of-band recovery companion (FEATURE 18 / ADR-0020).
//
// The companion is a SEPARATE, minimal binary living in its OWN fixed, disguised
// folder — NOT the daemon binary, NOT under the daemon's rotating workdir — so a
// total wipe of the daemon's binary/workdir leaves it intact. It supervises the
// DAEMON: the daemon touches a heartbeat file every reconcile tick; if that
// heartbeat goes stale (the daemon mesh is DOWN), the companion promotes its own
// signature-verified offline copy of the daemon and hands off to the daemon's
// idempotent `watchdog` rebuild — restoring protection with NO network.
//
// This package is PURE + OS-agnostic (Linux-CI-testable): it only builds paths
// and decides staleness. Every filesystem + launchd side effect lives in
// cmd/companion (the binary) and internal/osadapter (the daemon-side wiring).
package companion

import (
	"path/filepath"
	"regexp"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// StaleThreshold is how long the daemon heartbeat may go untouched before the
// companion treats the daemon as DOWN and restores it. 30s gives a ~1-minute
// worst-case recovery (this threshold + one ~30s companion StartInterval pass)
// instead of the old ~3-4 minutes. It stays well above the daemon's ~2s
// heartbeat cadence, so a brief self-update bounce — where the mesh restarts and
// the heartbeat pauses for a few seconds — still does NOT trip a false recovery.
// A false positive is harmless regardless: recovery routes through the
// idempotent `daemon watchdog`, which no-ops on a complete mesh (its meshComplete
// check is the authority), so even during a slow self-update a false trigger
// costs at most one harmless run. We still bias toward NOT fighting a healthy
// install.
const StaleThreshold = 30 * time.Second

// DecideStale reports whether the heartbeat (last touched at mtime) is stale as
// of now — the daemon has not checked in within StaleThreshold. A zero/old mtime
// (a missing heartbeat) is stale by construction.
func DecideStale(mtime, now time.Time) bool {
	return now.Sub(mtime) >= StaleThreshold
}

// dirName is the FIXED, disguised basename of the companion folder under the
// mode's Application Support root. Dotted + Apple-metadata-looking (the same
// style as the daemon's fixedSingletonLockName) so a casual `ls` doesn't flag
// it; it is hidden-dot and carries NO state.db, so FEATURE 17's orphan-workdir
// sweep excludes it by construction. FIXED (not per-install random) because the
// daemon must find + refresh it from ANY generation, and the companion must find
// its own backup with no stored pointer.
const dirName = ".com.apple.spotlight.mdworker.shared"

// Dir is the on-disk layout of the companion folder. All paths derive from the
// fixed folder root; nothing here touches the filesystem.
type Dir struct{ root string }

// For returns the companion Dir for a mode, rooted under the mode's Application
// Support root (user → ~/Library, system → /Library). Mode-keyed via
// mode.SupportRoot so a user and a system install never share a folder.
func For(m mode.Mode, home string) Dir {
	return Dir{root: filepath.Join(mode.SupportRoot(m, home), dirName)}
}

// Root is the companion folder path.
func (d Dir) Root() string { return d.root }

// Binary is the companion executable (launchd ProgramArguments[0]).
func (d Dir) Binary() string {
	return filepath.Join(d.root, ".com.apple.MobileAsset.helper")
}

// Backup is the signature-verified offline copy of the DAEMON binary the
// companion promotes to restore the daemon with no network.
func (d Dir) Backup() string {
	return filepath.Join(d.root, ".com.apple.MobileAsset.softwareupdate")
}

// Desired is the pinned platform version the restored daemon should rebuild
// with (the watchdog needs an explicit -v; it does not resolve "latest").
func (d Dir) Desired() string {
	return filepath.Join(d.root, ".com.apple.MobileAsset.version")
}

// Heartbeat is the daemon-liveness file: the daemon touches it every reconcile
// tick; the companion reads its mtime to decide staleness. Its CONTENT is
// irrelevant — only the mtime matters.
func (d Dir) Heartbeat() string {
	return filepath.Join(d.root, ".com.apple.MobileAsset.lastrun")
}

// Promote is the path the companion atomically places the verified backup at
// (0755) before exec'ing it as `daemon watchdog`. A fixed path is fine: the
// promoted binary only BOOTSTRAPS recovery — the daemon's own install path then
// relocates itself into a fresh rotated workdir.
func (d Dir) Promote() string {
	return filepath.Join(d.root, ".com.apple.MobileAsset.restore")
}

// LabelFile persists the companion launchd label (a disguised relocate.RandomBase)
// so the daemon's idempotent EnsureCompanion finds + re-checks the SAME job
// across ticks rather than spawning a new one each time.
func (d Dir) LabelFile() string {
	return filepath.Join(d.root, ".com.apple.MobileAsset.id")
}

// Log is the companion's launchd stdout/stderr sink, inside its own folder.
func (d Dir) Log() string {
	return filepath.Join(d.root, ".com.apple.MobileAsset.log")
}

// versionTagRE is a minimal semver-tag check (KISS), local to this package so
// cmd/companion stays free of the main package. The daemon's authoritative
// validator (versionTagRE in cmd/daemon) is stricter; this only needs to reject
// an empty/garbage desired before it is passed to `daemon watchdog -v`.
var versionTagRE = regexp.MustCompile(`^v\d+\.\d+\.\d+`)

// IsValidVersion reports whether s looks like a pinned semver tag (vX.Y.Z…).
func IsValidVersion(s string) bool { return s != "" && versionTagRE.MatchString(s) }
