//go:build darwin

package osadapter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/platdir"
)

// mkDaemonHome creates a directory under root and, when marked is set, stamps it
// with the DAEMON-HOME content sentinel (FEATURE 26 — the ONLY signal
// SweepOrphanWorkdirs gates a delete on). Unmarked dirs stand in for the watchdog
// copy dir and for real app folders — neither carries the magic.
func mkDaemonHome(t *testing.T, root, name string, marked bool) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if marked {
		platdir.MarkDaemonHome(dir)
	}
	return dir
}

// supportRootUnderHome points SupportRoot(User, …) at a t.TempDir() by setting
// HOME, and returns (home, supportRoot). User mode → ~/Library/Application
// Support.
func supportRootUnderHome(t *testing.T) (home, root string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	root = mode.SupportRoot(mode.User, home)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, root
}

// TestSweepOrphanWorkdirsRemovesOrphanKeepsKeep: an orphan daemon-home (magic, !=
// keep) is removed; the keep daemon-home is preserved.
func TestSweepOrphanWorkdirsRemovesOrphanKeepsKeep(t *testing.T) {
	_, root := supportRootUnderHome(t)

	keep := mkDaemonHome(t, root, "KeepVendorAgent", true)
	orphan := mkDaemonHome(t, root, "OrphanVendorAgent", true)

	removed, err := SweepOrphanWorkdirs(root, keep)
	if err != nil {
		t.Fatalf("SweepOrphanWorkdirs: unexpected error %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan workdir should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("keep workdir should survive, stat err = %v", err)
	}
}

// TestSweepOrphanWorkdirsSkipsNonSignatureDirs: dirs WITHOUT the daemon-home magic
// are left alone — the watchdog copy dir (no magic), and CRITICALLY a real app dir
// that merely holds a file named state.db (the OLD heuristic would have deleted it;
// the content-magic gate never does). Only the true magic-marked orphan is removed.
func TestSweepOrphanWorkdirsSkipsNonSignatureDirs(t *testing.T) {
	_, root := supportRootUnderHome(t)

	keep := mkDaemonHome(t, root, "KeepVendorAgent", true)
	watchdog := mkDaemonHome(t, root, "com.watchdog.copy", false) // no magic
	plainApp := mkDaemonHome(t, root, "SomeVendorApp", false)     // real app lookalike
	// The real-app lookalike even holds a state.db — the exact bait the removed
	// heuristic keyed on. It must STILL survive (no magic).
	if err := os.WriteFile(filepath.Join(plainApp, "state.db"), []byte("REAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	orphan := mkDaemonHome(t, root, "OrphanVendorAgent", true)

	removed, err := SweepOrphanWorkdirs(root, keep)
	if err != nil {
		t.Fatalf("SweepOrphanWorkdirs: unexpected error %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (only the orphan)", removed)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan should be removed, stat err = %v", err)
	}
	for name, dir := range map[string]string{"keep": keep, "watchdog": watchdog, "plainApp": plainApp} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s dir should survive, stat err = %v", name, err)
		}
	}
}

// TestSweepOrphanWorkdirsNoSupportRoot: a missing support root is not an error
// (nothing installed yet) — returns (0, nil).
func TestSweepOrphanWorkdirsNoSupportRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Do NOT create ~/Library/Application Support.

	root := mode.SupportRoot(mode.User, home)
	removed, err := SweepOrphanWorkdirs(root, filepath.Join(root, "keep"))
	if err != nil {
		t.Fatalf("missing support root should be (0,nil), got err %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}

// TestSweepOrphanWorkdirsGateAllowsNestedOrphan: a strictly-nested magic-marked
// orphan (not the keep, not an ancestor of it) is permitted by safeToRemoveWorkdir
// and removed.
func TestSweepOrphanWorkdirsGateAllowsNestedOrphan(t *testing.T) {
	_, root := supportRootUnderHome(t)
	orphan := mkDaemonHome(t, root, "LonelyOrphanAgent", true)

	keep := filepath.Join(root, "KeepVendorAgent") // a different (nonexistent) dir under root

	removed, err := SweepOrphanWorkdirs(root, keep)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("nested orphan should be removed, stat err = %v", err)
	}
}

// TestSweepOrphanWorkdirsTestModeCannotDeleteRealDir is the HF1 CRITICAL
// storage-separation regression. The scan root is an EXPLICIT param the caller
// (installMesh) fills with its sandbox local, so a test-mode sweep can never even
// SEE the real tree. The decoy real generation-workdir is a MARKED daemon-home
// (so it WOULD be swept if scanned) planted under mode.SupportRoot(mode.Test, HOME)
// — the exact directory the old bug scanned — and must SURVIVE because the sweep
// is scoped to a SEPARATE sandbox root.
func TestSweepOrphanWorkdirsTestModeCannotDeleteRealDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	realRoot := mode.SupportRoot(mode.Test, home)
	if err := os.MkdirAll(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	realGen := mkDaemonHome(t, realRoot, "RealInstallAgent", true) // decoy real daemon-home (marked)

	// The test-mode install lives in a SEPARATE sandbox root (spec.Workdir).
	sandbox := t.TempDir()
	sandboxDaemonHome := mkDaemonHome(t, sandbox, "daemon-home", true)
	sandboxOrphan := mkDaemonHome(t, sandbox, "SandboxOrphanAgent", true)

	removed, err := SweepOrphanWorkdirs(sandbox, sandboxDaemonHome)
	if err != nil {
		t.Fatalf("SweepOrphanWorkdirs: %v", err)
	}

	// THE REGRESSION ASSERTION: the decoy real-install workdir must SURVIVE.
	if _, serr := os.Stat(realGen); serr != nil {
		t.Fatalf("REGRESSION: real-install daemon-home under %q was deleted by a "+
			"test-mode sweep (stat err = %v)", realRoot, serr)
	}
	if _, serr := os.Stat(sandboxOrphan); !os.IsNotExist(serr) {
		t.Fatalf("sandbox orphan should be swept, stat err = %v", serr)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (only the sandbox orphan)", removed)
	}
}

// TestSafeToRemoveWorkdirBlocksBadTargets pins the safety belt SweepOrphanWorkdirs
// relies on: outside-root, the root itself, and an ancestor of keep are all
// refused; a strictly-nested non-keep dir is allowed.
func TestSafeToRemoveWorkdirBlocksBadTargets(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	keep := filepath.Join(root, "keepgen")
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(root, "orphan")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}

	if safeToRemoveWorkdir(outside, root, keep) {
		t.Fatal("outside-root dir must be refused")
	}
	if safeToRemoveWorkdir(root, root, keep) {
		t.Fatal("the support root itself must be refused")
	}
	if safeToRemoveWorkdir(keep, root, keep) {
		t.Fatal("the keep workdir must be refused")
	}
	if !safeToRemoveWorkdir(orphan, root, keep) {
		t.Fatal("a strictly-nested non-keep dir must be allowed")
	}
}
