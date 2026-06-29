//go:build darwin

package osadapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/companion"
	"github.com/eliteGoblin/focusd/daemon/internal/mode"
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
// hidden-dot sibling under the support root with NO state.db, so
// SweepOrphanWorkdirs (FEATURE 17 follow-up) never removes it while it sweeps a
// real orphan generation.
func TestCompanionFolderNotSwept(t *testing.T) {
	home, root := supportRootUnderHome(t)

	keep := mkWorkdir(t, root, ".keep.gen", true)
	orphan := mkWorkdir(t, root, ".orphan.gen", true)

	// The real companion folder, scaffolded with backup/binary/heartbeat but NO
	// state.db (the generation signature SweepOrphanWorkdirs keys on).
	dir := companion.For(mode.User, home)
	if err := os.MkdirAll(dir.Root(), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir.Binary(), dir.Backup(), dir.Heartbeat()} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := SweepOrphanWorkdirs(mode.User, keep)
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
