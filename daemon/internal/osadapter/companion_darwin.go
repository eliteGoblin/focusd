//go:build darwin

package osadapter

import (
	"bytes"
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
// (best-effort): ensure the folder; REFRESH the signed daemon backup + pinned
// desired; create the heartbeat baseline; materialize + REFRESH the embedded
// companion binary; and, if its launchd job is not loaded (or its binary just
// changed), (re)load it. daemonSelf is the path of a SIGNED daemon binary (the
// installer's own executable, or a running mesh member) used as the offline
// backup. Safe to call on every reconcile tick. Skipped entirely in Test mode
// (e2e never stands up the out-of-band rail).
//
// Both the backup and the companion binary are refreshed when they CHANGE, not
// only when absent. The prior write-only-if-missing checks meant that once a
// binary/backup existed from an earlier install it was FROZEN forever: an upgrade
// never landed the new embedded companion bytes (so a fix like #101's no-$HOME
// crash never reached the disk copy that actually runs) and the offline backup
// kept restoring a stale daemon. The companion binary is refreshed byte-exact
// (its refresh IS the fix, so nothing coarser may miss a change); the backup is
// size-gated here (a cheap backstop — RefreshCompanionBackup is the byte-exact
// authority on the real self-update rotation).
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
	// Backup: keep the offline signed-daemon copy in sync with the CURRENT running
	// signed daemon (daemonSelf) — refresh when ABSENT or when its SIZE differs, not
	// only when missing. This is the BACKSTOP for a daemon-binary swap that did not
	// route through self-update; the byte-exact authority is RefreshCompanionBackup
	// (self_update.go), which copies the freshly verified rotated bytes. Gate on a
	// cheap size compare so a healthy steady-state tick (~2s cadence) does two stats
	// instead of reading the multi-MB daemon binary on every pass: a rebuilt daemon
	// changes size, so an equal size means the backup is already current. daemonSelf
	// is a signed daemon binary, so a byte-for-byte copy stays a valid sig.VerifyFile
	// target; an unreadable/empty daemonSelf leaves a good backup INTACT rather than
	// clobbering it.
	if sfi, serr := os.Stat(daemonSelf); serr == nil && sfi.Size() > 0 {
		if bfi, berr := os.Stat(dir.Backup()); berr != nil || bfi.Size() != sfi.Size() {
			if data, rerr := os.ReadFile(daemonSelf); rerr == nil && len(data) > 0 {
				_ = companionWriteFile(dir.Backup(), data, 0o755)
			}
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
	// Companion binary + launchd job: materialize the embed, REFRESH it on change,
	// and force an immediate reload so an upgraded companion runs on the next tick.
	c := launchctlCtl{m: m}
	f := laFS{m: m}
	return ensureCompanionBinaryLoaded(dir, companionBinary, companionReloader{
		loaded:     c.loaded,
		bootout:    c.bootout,
		bootstrap:  c.bootstrap,
		plistPath:  f.plistPath,
		writePlist: f.write,
	}, companionInterval, time.Now())
}

// companionReloader is the injected launchd-control seam for the companion binary
// standup, so the write-on-change + forced-reload logic is unit-tested without a
// real launchctl. Production wires launchctlCtl + laFS.
type companionReloader struct {
	loaded     func(label string) bool
	bootout    func(label string) error
	bootstrap  func(pp string) error
	plistPath  func(label string) string
	writePlist func(path, content string) error
}

// companionReloaderCtl adapts a companionReloader's func fields to the controller
// interface robustReload expects (loaded/bootstrap/bootout), so the wedged-instance
// re-arm (#106-b2) reuses the SAME async-EIO-absorbing retry loop the mesh install
// uses (manager.go robustReload) instead of a bespoke bootout+bootstrap.
type companionReloaderCtl struct{ r companionReloader }

func (c companionReloaderCtl) loaded(label string) bool   { return c.r.loaded(label) }
func (c companionReloaderCtl) bootstrap(pp string) error  { return c.r.bootstrap(pp) }
func (c companionReloaderCtl) bootout(label string) error { return c.r.bootout(label) }

// ensureCompanionBinaryLoaded materializes the embedded companion binary and
// keeps its launchd job loaded, REFRESHING the on-disk binary whenever it differs
// from the embed. This is the upgrade fix: a prior install's companion was
// otherwise frozen forever by a write-only-if-missing check, so embed-side code
// fixes (e.g. #101's no-$HOME crash) never reached the disk copy that actually
// runs. The write is atomic (temp+rename), so a mid-run one-shot finishes on the
// old inode and execs the NEW inode next interval. When the binary actually
// CHANGED and its job is already loaded, it is booted out + re-bootstrapped so the
// fix runs on the NEXT tick rather than up to one StartInterval later; the bootout
// is best-effort (a not-loaded label simply has nothing to tear down). An
// unchanged, already-loaded companion is a pure no-op.
//
// #106-b2 (the companion's missing self-heal): a companion one-shot can get STUCK
// ALIVE after its blocking watchdog handoff. Its launchd job then reads loaded=true,
// so launchd never fires a fresh instance and the RanMarker freezes — the rail is
// dead while the daemon is healthy. The existing !loaded re-arm never triggers. So
// EVEN WHEN loaded==true, if the RanMarker is stale beyond companionRanRecentlyWindow
// (the companion fired then froze), the DAEMON replaces the wedged instance:
// bootout+bootstrap via robustReload. The actor is the DAEMON and the target is the
// COMPANION (different processes), so the #102-a self-SIGTERM hazard does NOT apply.
// A RearmMarker mtime throttles this to once per window so it can't churn a
// legitimately in-progress (slow) rebuild.
func ensureCompanionBinaryLoaded(dir companion.Dir, embed []byte, r companionReloader, intervalSec int, now time.Time) error {
	binChanged := false
	if fileContentDiffers(dir.Binary(), embed) {
		if werr := companionWriteFile(dir.Binary(), embed, 0o755); werr != nil {
			return werr
		}
		binChanged = true
	}
	// The label is persisted in the folder so we re-check the SAME job across ticks.
	label, lerr := ensureCompanionLabel(dir)
	if lerr != nil {
		return lerr
	}
	loaded := r.loaded(label)
	if binChanged && loaded {
		_ = r.bootout(label) // force reload of the changed binary; best-effort
		loaded = false
	}
	// #106-b2: replace a WEDGED-but-loaded companion (fired then froze). Gated to a
	// STALE-but-present RanMarker (b1 stamps it at pass start, so a wedged instance
	// leaves an EXISTING, frozen marker) and throttled by the RearmMarker so a
	// genuinely-slow rebuild is not churned every tick. A binChanged reload above
	// already re-armed the job, so this only runs when we did NOT just reload.
	if loaded && companionRanStale(dir, now) && !companionRearmedRecently(dir, now) {
		pp := r.plistPath(label)
		if werr := r.writePlist(pp, CompanionPlist(label, dir.Binary(), dir.Log(), intervalSec)); werr != nil {
			return werr
		}
		touchCompanionRearm(dir, now) // stamp the throttle BEFORE the (retrying) reload
		return robustReload(companionReloaderCtl{r}, label, pp, time.Sleep)
	}
	if loaded {
		return nil
	}
	pp := r.plistPath(label)
	if werr := r.writePlist(pp, CompanionPlist(label, dir.Binary(), dir.Log(), intervalSec)); werr != nil {
		return werr
	}
	return r.bootstrap(pp) // reuses the enable-trick + bootstrap
}

// companionRanStale reports whether the companion RanMarker EXISTS but is older than
// companionRanRecentlyWindow as of now — the wedged-alive signal (#106-b2): b1 stamps
// the marker at pass START, so a companion that fired and then FROZE in a blocking
// handoff leaves an existing, frozen marker. A MISSING marker is deliberately NOT
// stale here: it means the companion has not completed its first pass yet (a fresh
// bootstrap) or crash-loops before b1's touch — neither is the wedged-alive case the
// re-arm targets, and re-bootstrapping would not help.
func companionRanStale(dir companion.Dir, now time.Time) bool {
	fi, err := os.Stat(dir.RanMarker())
	if err != nil {
		return false // missing → not the wedged-alive case
	}
	return now.Sub(fi.ModTime()) >= companionRanRecentlyWindow
}

// companionRearmedRecently reports whether the daemon re-armed the companion (#106-b2)
// within the last companionRanRecentlyWindow — the throttle so a wedge re-bootstrap
// runs at most once per window (not every ~2s tick). A missing marker means never
// re-armed → not throttled.
func companionRearmedRecently(dir companion.Dir, now time.Time) bool {
	fi, err := os.Stat(dir.RearmMarker())
	if err != nil {
		return false
	}
	return now.Sub(fi.ModTime()) < companionRanRecentlyWindow
}

// touchCompanionRearm stamps the RearmMarker's mtime to now (best-effort) so the
// #106-b2 wedge re-arm is throttled to once per window. Mirrors touchRan: create the
// file if absent, then align its mtime to the injected clock for deterministic tests.
func touchCompanionRearm(dir companion.Dir, now time.Time) {
	p := dir.RearmMarker()
	if _, err := os.Stat(p); err != nil {
		_ = companionWriteFile(p, nil, 0o644)
	}
	_ = os.Chtimes(p, now, now)
}

// fileContentDiffers reports whether the file at path is ABSENT or its bytes
// differ from want. A cheap size compare short-circuits the multi-MB read on the
// common upgrade case (a rebuilt binary changes size); only an equal-size file is
// read back and byte-compared. Any stat/read error other than a clean equal match
// is treated as "differs" so callers re-materialize defensively rather than trust
// an unreadable/partial on-disk copy.
func fileContentDiffers(path string, want []byte) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return true // absent (or not a regular file) → (re)write
	}
	if fi.Size() != int64(len(want)) {
		return true // size gate: cheap "differs" without reading the file
	}
	got, rerr := os.ReadFile(path)
	if rerr != nil {
		return true
	}
	return !bytes.Equal(got, want)
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
	// Refresh the offline backup only when it is ABSENT or DIFFERS from the freshly
	// verified daemon bytes — the copy tracks the rotated binary without a needless
	// multi-MB rewrite on a no-op self-update.
	if fileContentDiffers(dir.Backup(), signedDaemonBytes) {
		if err := companionWriteFile(dir.Backup(), signedDaemonBytes, 0o755); err != nil {
			return err
		}
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

// companionRanRecentlyWindow is how recent the companion RanMarker's mtime must
// be for status to treat the rail as FIRING (issue #status-2). ~3× the 30s
// StartInterval, so a single missed pass does not flap the signal. A companion
// that never completes a recovery pass (e.g. the #101 no-$HOME crash) never
// touches the marker → this reads false → status honestly omits the rail line.
const companionRanRecentlyWindow = 90 * time.Second

// CompanionStatus reports the companion rail's liveness for status (bools only,
// no paths cross the boundary):
//   - present   — the companion binary is on disk AND its persisted launchd job
//     is LOADED (issue #status-2 (a): mere on-disk presence used to read "present"
//     even while the job was DOA);
//   - backupOK  — the offline daemon backup exists AND passes Ed25519 verification;
//   - ranRecently — the RanMarker shows a recovery pass COMPLETED within
//     companionRanRecentlyWindow (issue #status-2 (b): a silently-dead rail that
//     never fires reads false, so status omits the line instead of trusting
//     presence).
func CompanionStatus(m mode.Mode) (present, backupOK, ranRecently bool) {
	dir := companionDir(m)
	return companionStatus(dir, launchctlCtl{m: m}.loaded, sig.VerifyFile, time.Now())
}

// companionStatus is the seam-injected core of CompanionStatus, split out so the
// launchd-loaded + signature + firing checks are unit-tested without a real
// launchctl or the offline signing key. loadedFn probes launchd; verify checks the
// backup signature; now anchors the RanMarker staleness compare.
func companionStatus(
	dir companion.Dir,
	loadedFn func(string) bool,
	verify func(string) (bool, error),
	now time.Time,
) (present, backupOK, ranRecently bool) {
	binOnDisk := false
	if fi, err := os.Stat(dir.Binary()); err == nil && !fi.IsDir() {
		binOnDisk = true
	}
	present = binOnDisk && companionJobLoaded(dir, loadedFn)
	if ok, err := verify(dir.Backup()); err == nil && ok {
		backupOK = true
	}
	if fi, err := os.Stat(dir.RanMarker()); err == nil {
		ranRecently = now.Sub(fi.ModTime()) < companionRanRecentlyWindow
	}
	return present, backupOK, ranRecently
}

// companionJobLoaded reports whether the companion's persisted launchd label is
// currently loaded. A missing/empty label file (never bootstrapped) → false.
func companionJobLoaded(dir companion.Dir, loadedFn func(string) bool) bool {
	b, err := os.ReadFile(dir.LabelFile())
	if err != nil {
		return false
	}
	label := strings.TrimSpace(string(b))
	return label != "" && loadedFn(label)
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
//
// The temp file is UNIQUE per write (os.CreateTemp), not a fixed "<path>.tmp":
// EnsureCompanion refreshes the binary/backup on every mesh-worker tick where the
// content differs, so all mesh workers (RoleA/RoleB/RoleEnsure) rewrite the SAME
// target in lockstep right after an upgrade. A shared temp path would let one
// worker's rename race another's truncating write (renaming a torn file into
// place) or hit ENOENT on the second rename. A unique temp per writer isolates
// them: each writes + renames its own inode, and the last atomic rename wins with
// fully-formed content. The temp is a hidden-dot sibling (disguise-consistent) and
// is cleaned up on any failure before the rename.
func companionWriteFile(path string, b []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, werr := tmp.Write(b); werr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return werr
	}
	if cerr := tmp.Close(); cerr != nil {
		_ = os.Remove(tmpName)
		return cerr
	}
	if cerr := os.Chmod(tmpName, perm); cerr != nil {
		_ = os.Remove(tmpName)
		return cerr
	}
	if rerr := os.Rename(tmpName, path); rerr != nil {
		_ = os.Remove(tmpName)
		return rerr
	}
	return nil
}
