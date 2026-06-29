//go:build darwin

package osadapter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/companion"
	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/relocate"
	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

// FEATURE 18 / ADR-0020: the out-of-band recovery COMPANION — the daemon-side
// wiring that stands up, refreshes, and tears down the companion rail. The
// companion is a SEPARATE minimal binary in its OWN fixed disguised folder
// (companion.Dir, outside the daemon workdir) that recovers the daemon OFFLINE
// from a signed backup, on launchd (no Full Disk Access), SUPERSEDING the
// FEATURE 12 cron watchdog. This file is the darwin half; companion_other.go is
// the non-darwin no-op.
//
// Crucially the companion is DELIBERATELY NOT a mesh worker and its binary is
// NOT mesh-signed, so it is invisible to FindCurrentInstall + DiscoverAllGenerations
// and can never be retired/swept by FEATURE 17/19 cleanup (see CompanionPlist).

// companionInterval is the companion launchd StartInterval (seconds). ~30s: the
// companion is still a BACKSTOP, not a fast self-heal — the daemon's in-mesh
// reconcile already heals fast; the companion only matters once the whole daemon
// mesh is DOWN. Paired with the 30s StaleThreshold this gives a ~1-minute
// worst-case recovery (was ~3-4 min). Restore routes through the idempotent
// `daemon watchdog`, which no-ops a healthy mesh, so the tighter cadence cannot
// fight a slow self-update. RunAtLoad + StartInterval (a one-shot pass per
// interval), NOT KeepAlive.
const companionInterval = 30

// companionMinBytes is the floor on the embedded companion binary before it is
// written + loaded. The in-repo embed is a tiny PLACEHOLDER (see
// companiondata/companion); a real RELEASE replaces it with the built companion
// binary (multi-MB) before compiling the daemon (scripts/build-companion.sh).
// Until then EnsureCompanion still scaffolds the folder/backup/heartbeat but does
// NOT write a non-runnable placeholder or load a launchd job that could never
// exec. (Phase-1 honest deferral.)
const companionMinBytes = 1 << 20 // 1 MiB

func companionReady() bool { return len(companionBinary) >= companionMinBytes }

// companionDir returns the fixed companion folder for a mode.
func companionDir(m mode.Mode) companion.Dir {
	home, _ := os.UserHomeDir()
	return companion.For(m, home)
}

// EnsureCompanion idempotently stands up the out-of-band companion rail
// (best-effort): ensure the folder; refresh the signed daemon backup + pinned
// desired; create the heartbeat baseline; write the embedded companion binary if
// missing; and, if its launchd job is not loaded, enable + bootstrap it.
// daemonSelf is the path of a SIGNED daemon binary (the installer's own
// executable, or a running mesh member) used as the offline backup. Safe to call
// on every reconcile tick. Skipped entirely in Test mode (e2e never stands up
// the out-of-band rail).
func EnsureCompanion(m mode.Mode, daemonSelf, desired string) error {
	if m == mode.Test {
		return nil
	}
	if !companion.IsValidVersion(desired) {
		return fmt.Errorf("companion: refusing to ensure with invalid version %q", desired)
	}
	dir := companionDir(m)
	if err := os.MkdirAll(dir.Root(), 0o700); err != nil {
		return err
	}
	// Backup: place a signed daemon copy if missing (cheap on steady-state
	// ticks; RefreshCompanionBackup overwrites it on self-update).
	if _, err := os.Stat(dir.Backup()); os.IsNotExist(err) {
		if data, rerr := os.ReadFile(daemonSelf); rerr == nil {
			_ = companionWriteFile(dir.Backup(), data, 0o755)
		}
	}
	// Pinned desired version (cheap idempotent write).
	_ = companionWriteFile(dir.Desired(), []byte(desired), 0o644)
	// Heartbeat baseline: create it if missing so a freshly-installed daemon's
	// first companion run has an mtime to read (the daemon refreshes it each tick
	// via TouchCompanionHeartbeat).
	if _, err := os.Stat(dir.Heartbeat()); os.IsNotExist(err) {
		_ = companionWriteFile(dir.Heartbeat(), nil, 0o644)
	}
	if !companionReady() {
		// Placeholder embed (in-repo build): scaffold only. A real release embeds
		// the built companion binary; only THEN do we write + load it.
		return nil
	}
	// Companion binary: write the embedded bytes if missing.
	if _, err := os.Stat(dir.Binary()); os.IsNotExist(err) {
		if werr := companionWriteFile(dir.Binary(), companionBinary, 0o755); werr != nil {
			return werr
		}
	}
	// launchd job: load it iff not already loaded (idempotent). The label is
	// persisted in the folder so we re-check the SAME job across ticks.
	label, lerr := ensureCompanionLabel(dir)
	if lerr != nil {
		return lerr
	}
	c := launchctlCtl{m: m}
	if c.loaded(label) {
		return nil
	}
	f := laFS{m: m}
	pp := f.plistPath(label)
	if werr := f.write(pp, CompanionPlist(label, dir.Binary(), dir.Log(), companionInterval)); werr != nil {
		return werr
	}
	return c.bootstrap(pp) // reuses the enable-trick + bootstrap
}

// RefreshCompanionBackup overwrites the companion's offline daemon backup with
// freshly-verified daemon bytes + re-pins desired (best-effort). Called after a
// successful self-update so the offline copy tracks the rotated binary; must NOT
// fail the (already-completed) self-update. Skipped in Test mode.
func RefreshCompanionBackup(m mode.Mode, signedDaemonBytes []byte, desired string) error {
	if m == mode.Test {
		return nil
	}
	if len(signedDaemonBytes) == 0 {
		return fmt.Errorf("companion: refusing to refresh backup with empty bytes")
	}
	dir := companionDir(m)
	if err := os.MkdirAll(dir.Root(), 0o700); err != nil {
		return err
	}
	if err := companionWriteFile(dir.Backup(), signedDaemonBytes, 0o755); err != nil {
		return err
	}
	if companion.IsValidVersion(desired) {
		_ = companionWriteFile(dir.Desired(), []byte(desired), 0o644)
	}
	return nil
}

// RemoveCompanion tears down the companion rail: bootout its launchd job, remove
// the plist, and remove the whole companion folder. Best-effort — a leftover
// companion would rebuild the mesh AFTER a deliberate, gate-satisfied uninstall,
// which is wrong, so we try; but a failure must not fail the (completed) mesh
// teardown.
func RemoveCompanion(m mode.Mode) error {
	dir := companionDir(m)
	if b, err := os.ReadFile(dir.LabelFile()); err == nil {
		if label := strings.TrimSpace(string(b)); label != "" {
			c := launchctlCtl{m: m}
			f := laFS{m: m}
			_ = c.bootout(label)
			_ = f.remove(f.plistPath(label))
		}
	}
	return os.RemoveAll(dir.Root())
}

// CompanionStatus reports the companion rail's liveness for status (bools only,
// no paths cross the boundary): present — the companion binary is on disk;
// backupOK — the offline daemon backup exists AND passes Ed25519 verification.
func CompanionStatus(m mode.Mode) (present, backupOK bool) {
	dir := companionDir(m)
	if fi, err := os.Stat(dir.Binary()); err == nil && !fi.IsDir() {
		present = true
	}
	if ok, err := sig.VerifyFile(dir.Backup()); err == nil && ok {
		backupOK = true
	}
	return present, backupOK
}

// TouchCompanionHeartbeat updates the companion heartbeat's mtime to now — the
// daemon's liveness signal to the companion. Called on every successful reconcile
// tick. Creates the file (and folder) if missing. Skipped in Test mode.
func TouchCompanionHeartbeat(m mode.Mode) error {
	if m == mode.Test {
		return nil
	}
	dir := companionDir(m)
	if err := os.MkdirAll(dir.Root(), 0o700); err != nil {
		return err
	}
	hb := dir.Heartbeat()
	now := time.Now()
	if err := os.Chtimes(hb, now, now); err != nil {
		if os.IsNotExist(err) {
			return companionWriteFile(hb, nil, 0o644)
		}
		return err
	}
	return nil
}

// CompanionPlist renders the out-of-band companion's launchd plist (FEATURE 18 /
// ADR-0020). It is DELIBERATELY NOT a mesh worker:
//   - ProgramArguments is the companion binary ALONE (no role / --mesh argv).
//   - It emits NO EnvironmentVariables — in particular NO MeshEnvKey — so
//     DiscoverAllGenerations never buckets it as a mesh generation (live or
//     dead), and FEATURE 17/19 cleanup can never retire or sweep it.
//   - RunAtLoad + StartInterval (a one-shot pass per interval), NOT KeepAlive.
//   - ProcessType Background.
//
// Its binary is also NOT mesh-signed, so it never passes sig.VerifyFile and is
// invisible to FindCurrentInstall by construction. Pure → unit-tested.
func CompanionPlist(label, companionBin, logPath string, intervalSec int) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	sb.WriteString("<plist version=\"1.0\"><dict>\n")
	fmt.Fprintf(&sb, "  <key>Label</key><string>%s</string>\n", label)
	sb.WriteString("  <key>ProgramArguments</key><array>\n")
	fmt.Fprintf(&sb, "    <string>%s</string>\n", companionBin)
	sb.WriteString("  </array>\n")
	sb.WriteString("  <key>RunAtLoad</key><true/>\n")
	fmt.Fprintf(&sb, "  <key>StartInterval</key><integer>%d</integer>\n", intervalSec)
	sb.WriteString("  <key>ProcessType</key><string>Background</string>\n")
	fmt.Fprintf(&sb, "  <key>StandardErrorPath</key><string>%s</string>\n", logPath)
	fmt.Fprintf(&sb, "  <key>StandardOutPath</key><string>%s</string>\n", logPath)
	sb.WriteString("</dict></plist>\n")
	return sb.String()
}

// ensureCompanionLabel reads the persisted companion launchd label, generating +
// persisting a fresh disguised one (relocate.RandomBase) on first use. Stable
// across ticks so EnsureCompanion re-checks the SAME job rather than spawning a
// new one each time.
func ensureCompanionLabel(dir companion.Dir) (string, error) {
	if b, err := os.ReadFile(dir.LabelFile()); err == nil {
		if lbl := strings.TrimSpace(string(b)); lbl != "" {
			return lbl, nil
		}
	}
	label := relocate.RandomBase()
	if err := companionWriteFile(dir.LabelFile(), []byte(label), 0o644); err != nil {
		return "", err
	}
	return label, nil
}

// companionWriteFile writes b to path atomically (temp + rename) with perm,
// creating the parent dir. Mirrors core.atomicWrite (which is unexported in
// another package) so the companion scaffolding can't leave a half-written file.
func companionWriteFile(path string, b []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
