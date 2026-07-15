//go:build darwin

package osadapter

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/companion"
	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

// companionLabel is a stable disguised label for the companion plist in tests
// (production generates it once via relocate.RandomBase and persists it).
const companionLabel = "com.apple.MobileAsset.helper.cafe1234"

// TestCompanionPlistNotMeshWorker (HARD INVARIANT #2): the companion plist
// carries NO mesh-worker marker — no EnvironmentVariables / no MeshEnvKey and no
// --mesh argv — so isFocusdMeshWorkerPlist is false and generation cleanup never
// buckets it. It is also NOT KeepAlive (a one-shot StartInterval job).
func TestCompanionPlistNotMeshWorker(t *testing.T) {
	pl := CompanionPlist(companionLabel, "/x/.com.apple.MobileAsset.helper", "/x/.log", companionInterval)

	label, bin, argv, env := parsePlist(writeTempPlist(t, pl))
	if label != companionLabel {
		t.Fatalf("label = %q, want %q", label, companionLabel)
	}
	if len(argv) != 1 || bin != "/x/.com.apple.MobileAsset.helper" {
		t.Fatalf("ProgramArguments must be the companion binary ALONE, got argv=%v", argv)
	}
	if env != nil {
		t.Fatalf("companion plist must emit NO EnvironmentVariables, got %v", env)
	}
	if isFocusdMeshWorkerPlist(env, argv) {
		t.Fatalf("companion plist must NOT corroborate a mesh worker")
	}
	if strings.Contains(pl, "KeepAlive") {
		t.Fatalf("companion plist must not be KeepAlive:\n%s", pl)
	}
	if !strings.Contains(pl, "<key>StartInterval</key>") {
		t.Fatalf("companion plist must carry a StartInterval:\n%s", pl)
	}
	if strings.Contains(pl, MeshEnvKey) {
		t.Fatalf("companion plist must not contain the mesh env key:\n%s", pl)
	}
}

// writeTempPlist writes content to a temp .plist and returns the path.
func writeTempPlist(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.plist")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCompanionNotDiscovered (HARD INVARIANT #1 + #2): a companion-style plist in
// the LaunchDir scan is NEVER bucketed as a focusd generation.
//
//   - #1: the companion binary is NOT mesh-signed, so verify returns false → the
//     scan's `default: continue` skips it entirely (neither live nor dead).
//   - #2: even if it (hypothetically) verified, it carries no mesh-worker marker,
//     so it is never returned as a live generation.
//
// Both the discovery scan (generations) and FindCurrentInstall ignore it.
func TestCompanionNotDiscovered(t *testing.T) {
	home, laDir := laDirUnderHome(t)

	// Place a real, signed mesh generation so the scan has SOMETHING to find —
	// the companion must not perturb it.
	roster := []string{"com.apple.metadata.helper.1111", "com.google.keystone.daemon.1112", "org.mozilla.updater.agent.1113"}
	meshBin, _ := writeGeneration(t, home, laDir, "gen1", roster)

	// Companion binary lives in its own folder with a real on-disk file (so the
	// "deleted binary" dead-generation path is NOT taken — it is present but
	// simply not mesh-signed).
	dir := companion.For(mode.User, home)
	if err := os.MkdirAll(dir.Root(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir.Binary(), []byte("NOT-A-MESH-SIGNED-BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	pp := filepath.Join(laDir, companionLabel+".plist")
	if err := os.WriteFile(pp, []byte(CompanionPlist(companionLabel, dir.Binary(), dir.Log(), companionInterval)), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("unsigned companion is continue'd by discovery", func(t *testing.T) {
		verify := Verifier(func(p string) (bool, error) { return p == meshBin, nil }) // only the mesh verifies
		live, dead, err := DiscoverAllGenerations(mode.User, verify)
		if err != nil {
			t.Fatalf("DiscoverAllGenerations: %v", err)
		}
		if len(dead) != 0 {
			t.Fatalf("companion (present, unsigned) must not be a dead generation, got %+v", dead)
		}
		if len(live) != 1 || live[0].BinaryPath != meshBin {
			t.Fatalf("want exactly the mesh generation, got %+v", live)
		}
		for _, g := range live {
			for _, lbl := range g.Labels {
				if lbl == companionLabel {
					t.Fatalf("companion label was bucketed into a generation: %v", g.Labels)
				}
			}
		}
	})

	t.Run("even if it verified, no mesh marker ⇒ not a generation", func(t *testing.T) {
		// Hypothetical: pretend the companion binary DID verify. Without a mesh
		// worker marker (#2) it must still not be returned as a live generation.
		verify := Verifier(func(p string) (bool, error) { return p == meshBin || p == dir.Binary(), nil })
		live, _, err := DiscoverAllGenerations(mode.User, verify)
		if err != nil {
			t.Fatalf("DiscoverAllGenerations: %v", err)
		}
		for _, g := range live {
			if g.BinaryPath == dir.Binary() {
				t.Fatalf("companion binary was returned as a live generation despite no mesh marker")
			}
		}
	})

	t.Run("FindCurrentInstall ignores the companion", func(t *testing.T) {
		verify := Verifier(func(p string) (bool, error) { return p == meshBin, nil })
		cur, err := FindCurrentInstall(mode.User, verify)
		if err != nil {
			t.Fatalf("FindCurrentInstall: %v", err)
		}
		if cur.BinaryPath != meshBin {
			t.Fatalf("FindCurrentInstall.BinaryPath = %q, want the mesh binary", cur.BinaryPath)
		}
		for _, lbl := range cur.Labels {
			if lbl == companionLabel {
				t.Fatalf("companion label leaked into the discovered install: %v", cur.Labels)
			}
		}
	})
}

// TestCompanionFolderNotSwept (HARD INVARIANT #2): the companion folder is a
// sibling under the support root with NO daemon-home content sentinel, so
// SweepOrphanWorkdirs (FEATURE 26 content gate) never removes it while it sweeps a
// real orphan generation.
func TestCompanionFolderNotSwept(t *testing.T) {
	home, root := supportRootUnderHome(t)

	keep := mkDaemonHome(t, root, "KeepVendorAgent", true)
	orphan := mkDaemonHome(t, root, "OrphanVendorAgent", true)

	// The real companion folder, scaffolded with backup/binary/heartbeat but NO
	// daemon-home magic (the signature SweepOrphanWorkdirs keys on).
	dir := companion.For(mode.User, home)
	if err := os.MkdirAll(dir.Root(), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir.Binary(), dir.Backup(), dir.Heartbeat()} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := SweepOrphanWorkdirs(root, keep)
	if err != nil {
		t.Fatalf("SweepOrphanWorkdirs: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (only the orphan)", removed)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(dir.Root()); err != nil {
		t.Fatalf("companion folder must survive the sweep, stat err = %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("keep workdir must survive, stat err = %v", err)
	}
}

// TestEnsureCompanionScaffoldsWithoutLaunchd: with the in-repo PLACEHOLDER embed
// (companionReady() == false), EnsureCompanion scaffolds the folder, backup,
// desired, and heartbeat WITHOUT writing the companion binary or touching
// launchd — and is idempotent. RemoveCompanion then tears the folder down.
func TestEnsureCompanionScaffoldsWithoutLaunchd(t *testing.T) {
	if companionReady() {
		t.Skip("a real companion binary is embedded; the scaffold-only path is N/A")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	// A signed-daemon stand-in to be copied into the backup.
	selfBin := filepath.Join(home, "daemon-self")
	if err := os.WriteFile(selfBin, []byte("SIGNED-DAEMON-BYTES"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := EnsureCompanion(mode.User, selfBin, "v0.16.3"); err != nil {
		t.Fatalf("EnsureCompanion: %v", err)
	}
	dir := companion.For(mode.User, home)
	// Backup == daemonSelf bytes.
	got, err := os.ReadFile(dir.Backup())
	if err != nil || string(got) != "SIGNED-DAEMON-BYTES" {
		t.Fatalf("backup = %q (err %v), want the daemon bytes", got, err)
	}
	if b, _ := os.ReadFile(dir.Desired()); string(b) != "v0.16.3" {
		t.Fatalf("desired = %q, want v0.16.3", b)
	}
	if _, err := os.Stat(dir.Heartbeat()); err != nil {
		t.Fatalf("heartbeat baseline not created: %v", err)
	}
	// Placeholder embed ⇒ NO companion binary, NO plist, NO label file.
	if _, err := os.Stat(dir.Binary()); !os.IsNotExist(err) {
		t.Fatalf("companion binary written despite placeholder embed (stat err %v)", err)
	}
	if _, err := os.Stat(dir.LabelFile()); !os.IsNotExist(err) {
		t.Fatalf("label file written despite placeholder embed (stat err %v)", err)
	}

	// Idempotent: a second call must not error.
	if err := EnsureCompanion(mode.User, selfBin, "v0.16.3"); err != nil {
		t.Fatalf("EnsureCompanion (second) : %v", err)
	}

	// Heartbeat touch advances mtime.
	if err := TouchCompanionHeartbeat(mode.User); err != nil {
		t.Fatalf("TouchCompanionHeartbeat: %v", err)
	}

	// RemoveCompanion deletes the whole folder.
	if err := RemoveCompanion(mode.User); err != nil {
		t.Fatalf("RemoveCompanion: %v", err)
	}
	if _, err := os.Stat(dir.Root()); !os.IsNotExist(err) {
		t.Fatalf("companion folder should be gone after RemoveCompanion, stat err = %v", err)
	}
}

// TestCompanionStatusCore exercises the seam-injected core of CompanionStatus
// (issue #status-2). present now requires the binary on disk AND the launchd job
// LOADED (not mere on-disk presence — the old bug that read "present" while the
// job was DOA); backupOK tracks Ed25519 verification of the offline backup; and
// ranRecently tracks the RanMarker firing signal. Injected loadedFn/verify keep it
// deterministic without a real launchctl or the offline signing key.
func TestCompanionStatusCore(t *testing.T) {
	home := t.TempDir()
	dir := companion.For(mode.User, home)
	if err := os.MkdirAll(dir.Root(), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	loadedTrue := func(string) bool { return true }
	loadedFalse := func(string) bool { return false }
	verifyReal := func(p string) (bool, error) { return sig.VerifyFile(p) }

	// Empty folder: nothing present, nothing verifies, never fired.
	if p, b, r := companionStatus(dir, loadedTrue, verifyReal, now); p || b || r {
		t.Fatalf("empty companion folder: want (false,false,false), got (%v,%v,%v)", p, b, r)
	}

	// Binary on disk + a persisted label — but the job is NOT loaded → present
	// stays false (the #status-2 (a) fix: on-disk presence alone is not "present").
	if err := os.WriteFile(dir.Binary(), []byte("companion-bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir.LabelFile(), []byte("com.apple.MobileAsset.helper.dead"), 0o644); err != nil {
		t.Fatal(err)
	}
	unsigned := append([]byte("not a signed daemon binary"), make([]byte, sig.SigLen)...)
	if err := os.WriteFile(dir.Backup(), unsigned, 0o755); err != nil {
		t.Fatal(err)
	}
	if p, _, _ := companionStatus(dir, loadedFalse, verifyReal, now); p {
		t.Fatalf("binary on disk but job NOT loaded → present must be false")
	}

	// Job loaded → present=true. Unsigned backup → backupOK=false.
	p, b, _ := companionStatus(dir, loadedTrue, verifyReal, now)
	if !p {
		t.Fatalf("binary on disk + label loaded → present must be true")
	}
	if b {
		t.Fatalf("an unsigned backup must NOT verify → backupOK must be false")
	}

	// RanMarker firing signal: a recent marker reads true; a stale one reads false.
	if err := os.WriteFile(dir.RanMarker(), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, r := companionStatus(dir, loadedTrue, verifyReal, now); !r {
		t.Fatalf("a freshly-touched RanMarker must read ranRecently=true")
	}
	stale := now.Add(-companionRanRecentlyWindow - time.Minute)
	if err := os.Chtimes(dir.RanMarker(), stale, stale); err != nil {
		t.Fatal(err)
	}
	if _, _, r := companionStatus(dir, loadedTrue, verifyReal, now); r {
		t.Fatalf("a stale RanMarker (older than the window) must read ranRecently=false")
	}
}

// TestFileContentDiffers exercises the content-aware refresh predicate that
// replaces the old write-only-if-missing checks: absent / different-size /
// different-content → differs (rewrite); byte-identical → does NOT differ (no-op).
// A directory path reads as differs (defensive — never trust an unreadable copy).
func TestFileContentDiffers(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "f")
	want := []byte("hello-world-companion-bytes")

	if !fileContentDiffers(p, want) {
		t.Fatalf("an absent file must read as differs")
	}
	if err := os.WriteFile(p, want, 0o644); err != nil {
		t.Fatal(err)
	}
	if fileContentDiffers(p, want) {
		t.Fatalf("byte-identical content must NOT differ")
	}
	sameLen := append([]byte(nil), want...)
	sameLen[0] ^= 0xff
	if !fileContentDiffers(p, sameLen) {
		t.Fatalf("same-length, different-content must differ")
	}
	if !fileContentDiffers(p, append(want, 'x')) {
		t.Fatalf("different-length must differ")
	}
	if !fileContentDiffers(tmp, want) {
		t.Fatalf("a directory path must read as differs")
	}
}

// reloadCalls records the injected launchd-control calls made by
// ensureCompanionBinaryLoaded so the write-on-change + forced-reload contract is
// asserted without a real launchctl.
type reloadCalls struct{ bootout, bootstrap, plistWrite int }

func newTestReloader(launchDir string, loaded *bool, c *reloadCalls) companionReloader {
	return companionReloader{
		loaded:  func(string) bool { return *loaded },
		bootout: func(string) error { c.bootout++; *loaded = false; return nil },
		bootstrap: func(string) error {
			c.bootstrap++
			*loaded = true
			return nil
		},
		plistPath: func(label string) string { return filepath.Join(launchDir, label+".plist") },
		writePlist: func(path, content string) error {
			c.plistWrite++
			return os.WriteFile(path, []byte(content), 0o644)
		},
	}
}

// TestEnsureCompanionBinaryLoadedRefreshAndReload is the core of the upgrade fix:
// the embedded companion is materialized and its on-disk copy is REFRESHED when
// the embed changes (the old write-only-if-missing froze a prior install's
// companion forever, so embed-side code fixes like #101 never reached disk), and
// a changed binary that is already loaded is force-reloaded (bootout+bootstrap) so
// the fix runs next tick — while an identical embed is a pure no-op.
func TestEnsureCompanionBinaryLoadedRefreshAndReload(t *testing.T) {
	home := t.TempDir()
	dir := companion.For(mode.User, home)
	if err := os.MkdirAll(dir.Root(), 0o755); err != nil {
		t.Fatal(err)
	}
	launchDir := t.TempDir()
	embedV1 := bytes.Repeat([]byte("A"), 2048)
	embedV2 := bytes.Repeat([]byte("B"), 4096) // different size AND content

	loaded := false

	// Fresh install (binary absent) + job not loaded → write embed, bootstrap, and
	// NO bootout (nothing was loaded to tear down).
	var c1 reloadCalls
	if err := ensureCompanionBinaryLoaded(dir, embedV1, newTestReloader(launchDir, &loaded, &c1), 30); err != nil {
		t.Fatalf("ensureCompanionBinaryLoaded (fresh): %v", err)
	}
	if got, _ := os.ReadFile(dir.Binary()); !bytes.Equal(got, embedV1) {
		t.Fatalf("companion binary not materialized to the embed")
	}
	if c1.bootout != 0 {
		t.Fatalf("no job was loaded — bootout must not be called, got %+v", c1)
	}
	if c1.bootstrap != 1 || c1.plistWrite != 1 {
		t.Fatalf("fresh install must write plist + bootstrap once, got %+v", c1)
	}
	if !loaded {
		t.Fatalf("bootstrap should have marked the job loaded")
	}

	// Second pass, identical embed, job now loaded → PURE no-op (no write, no
	// bootout, no bootstrap, no plist rewrite).
	var c2 reloadCalls
	if err := ensureCompanionBinaryLoaded(dir, embedV1, newTestReloader(launchDir, &loaded, &c2), 30); err != nil {
		t.Fatalf("ensureCompanionBinaryLoaded (identical): %v", err)
	}
	if (c2 != reloadCalls{}) {
		t.Fatalf("identical embed + loaded must be a pure no-op, got %+v", c2)
	}
	if got, _ := os.ReadFile(dir.Binary()); !bytes.Equal(got, embedV1) {
		t.Fatalf("companion binary must not change on a no-op tick")
	}

	// Embed CHANGED (upgrade) while the job is loaded → refresh the on-disk binary
	// AND force an immediate reload (bootout then bootstrap + plist rewrite).
	var c3 reloadCalls
	if err := ensureCompanionBinaryLoaded(dir, embedV2, newTestReloader(launchDir, &loaded, &c3), 30); err != nil {
		t.Fatalf("ensureCompanionBinaryLoaded (changed): %v", err)
	}
	if got, _ := os.ReadFile(dir.Binary()); !bytes.Equal(got, embedV2) {
		t.Fatalf("companion binary not refreshed to the new embed (the frozen-companion bug)")
	}
	if c3.bootout != 1 {
		t.Fatalf("a changed binary that is loaded must be booted out to force reload, got %+v", c3)
	}
	if c3.bootstrap != 1 || c3.plistWrite != 1 {
		t.Fatalf("forced reload must rewrite the plist + bootstrap once, got %+v", c3)
	}
	if !loaded {
		t.Fatalf("job must be loaded again after the forced reload")
	}
}

// inodeOf returns the inode of path (darwin) — a temp+rename replacement changes
// the inode, so a stable inode proves the content-gate skipped the write.
func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Sys().(*syscall.Stat_t).Ino
}

// TestEnsureCompanionRefreshesBackupOnChange: the offline daemon backup must
// track the CURRENT running signed daemon — refreshed when it changes, not only
// when missing (the frozen-backup bug that restored a 16-day-old daemon). The
// EnsureCompanion backstop is size-gated (a rebuilt daemon changes size;
// RefreshCompanionBackup is the byte-exact authority on self-update), so this
// exercises size-different upgrades. An unreadable daemonSelf must LEAVE a good
// backup intact rather than clobber it.
func TestEnsureCompanionRefreshesBackupOnChange(t *testing.T) {
	if companionReady() {
		t.Skip("a real companion binary is embedded; the scaffold-only backup path is N/A")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := companion.For(mode.User, home)

	selfBin := filepath.Join(home, "daemon-self")
	v1 := []byte("SIGNED-DAEMON-BYTES-V1")
	if err := os.WriteFile(selfBin, v1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCompanion(mode.User, selfBin, "v0.16.3"); err != nil {
		t.Fatalf("EnsureCompanion (v1): %v", err)
	}
	if b, _ := os.ReadFile(dir.Backup()); !bytes.Equal(b, v1) {
		t.Fatalf("backup = %q, want v1", b)
	}

	// Upgrade: the running signed daemon changes → the backup must REFRESH.
	v2 := []byte("SIGNED-DAEMON-BYTES-V2-DIFFERENT-LENGTH")
	if err := os.WriteFile(selfBin, v2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCompanion(mode.User, selfBin, "v0.16.3"); err != nil {
		t.Fatalf("EnsureCompanion (v2): %v", err)
	}
	if b, _ := os.ReadFile(dir.Backup()); !bytes.Equal(b, v2) {
		t.Fatalf("backup not refreshed on upgrade: got %q, want v2 (frozen-backup bug)", b)
	}

	// Idempotent: identical self → backup unchanged (same inode, no rewrite).
	before := inodeOf(t, dir.Backup())
	if err := EnsureCompanion(mode.User, selfBin, "v0.16.3"); err != nil {
		t.Fatalf("EnsureCompanion (idempotent): %v", err)
	}
	if inodeOf(t, dir.Backup()) != before {
		t.Fatalf("identical self churned the backup (rewrote an unchanged file)")
	}

	// Unreadable self → a good backup must be LEFT INTACT (never clobbered).
	if err := EnsureCompanion(mode.User, filepath.Join(home, "nope"), "v0.16.3"); err != nil {
		t.Fatalf("EnsureCompanion (unreadable self): %v", err)
	}
	if b, _ := os.ReadFile(dir.Backup()); !bytes.Equal(b, v2) {
		t.Fatalf("backup clobbered when daemonSelf was unreadable: got %q", b)
	}
}

// TestRefreshCompanionBackupWriteWhenChanged: the self-update backup refresh is
// write-when-changed — empty bytes are refused, an identical refresh is a no-op
// (same inode), and changed bytes replace the backup.
func TestRefreshCompanionBackupWriteWhenChanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := companion.For(mode.User, home)

	if err := RefreshCompanionBackup(mode.User, nil, "v0.16.3"); err == nil {
		t.Fatalf("empty bytes must be refused")
	}

	v1 := []byte("SIGNED-DAEMON-BYTES-V1")
	if err := RefreshCompanionBackup(mode.User, v1, "v0.16.3"); err != nil {
		t.Fatalf("RefreshCompanionBackup (v1): %v", err)
	}
	if b, _ := os.ReadFile(dir.Backup()); !bytes.Equal(b, v1) {
		t.Fatalf("backup not written on first refresh")
	}
	before := inodeOf(t, dir.Backup())

	// Identical → no rewrite (content-gate skips it).
	if err := RefreshCompanionBackup(mode.User, v1, "v0.16.3"); err != nil {
		t.Fatalf("RefreshCompanionBackup (identical): %v", err)
	}
	if inodeOf(t, dir.Backup()) != before {
		t.Fatalf("an identical refresh rewrote the backup")
	}

	// Changed → replaced.
	v2 := []byte("SIGNED-DAEMON-BYTES-V2-LONGER")
	if err := RefreshCompanionBackup(mode.User, v2, "v0.16.3"); err != nil {
		t.Fatalf("RefreshCompanionBackup (v2): %v", err)
	}
	if b, _ := os.ReadFile(dir.Backup()); !bytes.Equal(b, v2) {
		t.Fatalf("backup not refreshed to v2")
	}
}

// TestCompanionWriteFileConcurrent guards the atomic-write helper against the
// shared-temp-path race that this change makes routine: EnsureCompanion now
// refreshes the companion binary/backup on EVERY mesh-worker tick where content
// differs, so all mesh workers (RoleA/RoleB/RoleEnsure) rewrite the SAME target in
// lockstep right after an upgrade. A fixed "<path>.tmp" collides — one worker's
// rename races another's truncating write, and a second rename hits ENOENT — so a
// unique temp per write must let every concurrent writer succeed and leave exactly
// the intended content with no leftover temp files.
func TestCompanionWriteFileConcurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".target")
	content := bytes.Repeat([]byte("companion-bytes"), 512)

	const n = 16
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- companionWriteFile(path, content, 0o755)
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatalf("concurrent companionWriteFile failed (shared-temp race?): %v", e)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("final content wrong after concurrent writes: err=%v", err)
	}
	if fi, _ := os.Stat(path); fi != nil && fi.Mode().Perm() != 0o755 {
		t.Fatalf("perm = %o, want 0755", fi.Mode().Perm())
	}
	// No leftover temp files — every temp was renamed into place (atomic) or
	// cleaned up.
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.Name() != ".target" {
			t.Fatalf("leftover temp file after concurrent writes: %q", e.Name())
		}
	}
}

// TestEnsureCompanionRejectsInvalidVersion: an empty/garbage desired is refused
// (a companion that pinned a bad version could never restore a usable mesh).
func TestEnsureCompanionRejectsInvalidVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, bad := range []string{"", "latest", "1.2.3"} {
		if err := EnsureCompanion(mode.User, "/nope", bad); err == nil {
			t.Fatalf("EnsureCompanion(desired=%q) = nil, want error", bad)
		}
	}
}

// TestEnsureCompanionTestModeNoOp: Test mode never stands up the out-of-band
// rail (e2e stays self-contained).
func TestEnsureCompanionTestModeNoOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := EnsureCompanion(mode.Test, "/whatever", "v1.2.3"); err != nil {
		t.Fatalf("EnsureCompanion(Test) = %v, want nil", err)
	}
	if _, err := os.Stat(companion.For(mode.Test, home).Root()); !os.IsNotExist(err) {
		t.Fatalf("Test mode must not scaffold a companion folder")
	}
}
