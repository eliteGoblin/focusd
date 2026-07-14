//go:build darwin

package osadapter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// mkWorkdir creates a hidden-dot directory under root and, when withDB is set,
// drops a state.db file inside it (the generation-workdir signature). Returns
// the dir path.
func mkWorkdir(t *testing.T, root, name string, withDB bool) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if withDB {
		if err := os.WriteFile(filepath.Join(dir, stateDBFile), []byte("FAKE-DB"), 0o644); err != nil {
			t.Fatal(err)
		}
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

// TestSweepOrphanWorkdirsRemovesOrphanKeepsKeep: an orphan generation workdir
// (hidden-dot + state.db, != keep) is removed; the keep workdir is preserved.
func TestSweepOrphanWorkdirsRemovesOrphanKeepsKeep(t *testing.T) {
	_, root := supportRootUnderHome(t)

	keep := mkWorkdir(t, root, ".keep.gen", true)
	orphan := mkWorkdir(t, root, ".orphan.gen", true)

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

// TestSweepOrphanWorkdirsSkipsNonSignatureDirs: dirs that are NOT the generation
// signature are left alone — a hidden-dot dir WITHOUT state.db (the watchdog
// copy dir), and a non-hidden dir WITH state.db (a legit app dir is never
// hidden-dot). Only the true orphan is removed.
func TestSweepOrphanWorkdirsSkipsNonSignatureDirs(t *testing.T) {
	_, root := supportRootUnderHome(t)

	keep := mkWorkdir(t, root, ".keep.gen", true)
	watchdog := mkWorkdir(t, root, ".watchdog.copy", false) // hidden-dot, NO state.db
	plainApp := mkWorkdir(t, root, "SomeVendorApp", true)   // state.db but not hidden-dot
	orphan := mkWorkdir(t, root, ".orphan.gen", true)

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
	removed, err := SweepOrphanWorkdirs(root, filepath.Join(root, ".keep"))
	if err != nil {
		t.Fatalf("missing support root should be (0,nil), got err %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}

// TestSweepOrphanWorkdirsGateBlocksKeep: even if the keep path were somehow a
// candidate, the keep-exclusion + safeToRemoveWorkdir belt prevents deleting it.
// Here we sweep with an EMPTY keep + a state.db-bearing dir whose path is the
// support root's only child; safeToRemoveWorkdir still permits a strictly-nested
// orphan, so it is removed — confirming the gate allows legitimate targets while
// the prior tests confirm it blocks the keep.
func TestSweepOrphanWorkdirsGateAllowsNestedOrphan(t *testing.T) {
	_, root := supportRootUnderHome(t)
	orphan := mkWorkdir(t, root, ".lonely.orphan", true)

	// keepWorkdir under root but a different dir → orphan is strictly-nested,
	// not the keep, not an ancestor → safeToRemoveWorkdir permits removal.
	keep := filepath.Join(root, ".keep.gen")

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
// storage-separation regression. BEFORE the fix, SweepOrphanWorkdirs recomputed
// its scan root as mode.SupportRoot(m, home) — and mode.SupportRoot(mode.Test, …)
// resolves to the REAL ~/Library/Application Support (mode.go only special-cases
// System). So a `daemon install --test-mode` swept and DELETED real-install
// generation workdirs under the operator's actual HOME. The scan root is now an
// EXPLICIT param the caller (installMesh) fills with its sandbox local, so a
// test-mode sweep can never even SEE the real tree.
//
// The test plants a decoy "real install" generation-workdir (hidden-dot +
// state.db) under mode.SupportRoot(mode.Test, HOME) — the exact directory the old
// bug scanned — and runs the sweep scoped to a SEPARATE sandbox root. The decoy
// must SURVIVE; only the sandbox orphan is swept.
func TestSweepOrphanWorkdirsTestModeCannotDeleteRealDir(t *testing.T) {
	// HOME points at a decoy tree. mode.SupportRoot(mode.Test, HOME) resolves
	// HERE — the real-install location the pre-fix sweep would have scanned.
	home := t.TempDir()
	t.Setenv("HOME", home)
	realRoot := mode.SupportRoot(mode.Test, home)
	if err := os.MkdirAll(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	realGen := mkWorkdir(t, realRoot, ".real.install.gen", true) // decoy real generation-workdir

	// The test-mode install lives in a SEPARATE sandbox root (spec.Workdir),
	// with its own daemon-home + a genuine orphan generation-workdir.
	sandbox := t.TempDir()
	sandboxDaemonHome := mkWorkdir(t, sandbox, ".daemon-home", false)
	sandboxOrphan := mkWorkdir(t, sandbox, ".sandbox.orphan.gen", true)

	// installMesh now passes its sandboxed supportRoot local — NOT mode.SupportRoot.
	removed, err := SweepOrphanWorkdirs(sandbox, sandboxDaemonHome)
	if err != nil {
		t.Fatalf("SweepOrphanWorkdirs: %v", err)
	}

	// THE REGRESSION ASSERTION: the decoy real-install workdir must SURVIVE.
	if _, serr := os.Stat(realGen); serr != nil {
		t.Fatalf("REGRESSION: real-install generation workdir under %q was deleted by a "+
			"test-mode sweep (stat err = %v)", realRoot, serr)
	}
	// The sandbox orphan is the only thing swept.
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
