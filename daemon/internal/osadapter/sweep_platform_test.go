package osadapter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/platdir"
)

// mkPlatformWorkdir creates a dir under root and marks it as a platform-workdir
// (the CONTENT sentinel SweepStalePlatformWorkdirs keys on, FEATURE 26). Names no
// longer need a leading dot — recognition is by content, not by name.
func mkPlatformWorkdir(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	platdir.MarkPlatformWorkdir(dir)
	return dir
}

// TestSweepStalePlatformWorkdirs: stale platform-workdirs (content sentinel) are
// removed; the live one (keep) survives; a daemon-home (DIFFERENT magic) is never
// a candidate; and a real app folder (NO magic) is never touched.
func TestSweepStalePlatformWorkdirs(t *testing.T) {
	root := t.TempDir()

	keep := mkPlatformWorkdir(t, root, "LiveEngineStore")
	stale := mkPlatformWorkdir(t, root, "StaleEngineStore")

	// A daemon-home: carries the DAEMON-HOME magic (not the platform magic).
	daemonHome := filepath.Join(root, "com.acme.helper")
	if err := os.MkdirAll(daemonHome, 0o700); err != nil {
		t.Fatal(err)
	}
	platdir.MarkDaemonHome(daemonHome)
	if err := os.WriteFile(filepath.Join(daemonHome, "version.json"), []byte(`{"desired":"v1"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// A REAL app folder lookalike: no magic at all, even with app-ish content.
	realApp := filepath.Join(root, "Google")
	if err := os.MkdirAll(realApp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realApp, "state.db"), []byte("REAL SQLITE"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := SweepStalePlatformWorkdirs(root, keep)
	if err != nil {
		t.Fatalf("SweepStalePlatformWorkdirs: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (only the stale platform-workdir)", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale platform-workdir should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("live platform-workdir (keep) must survive: %v", err)
	}
	if _, err := os.Stat(daemonHome); err != nil {
		t.Fatalf("daemon-home (different magic) must NEVER be swept here: %v", err)
	}
	if _, err := os.Stat(realApp); err != nil {
		t.Fatalf("real app folder (no magic) must NEVER be swept: %v", err)
	}
}

// TestSweepStalePlatformWorkdirsNoRoot: a missing support root is not an error.
func TestSweepStalePlatformWorkdirsNoRoot(t *testing.T) {
	removed, err := SweepStalePlatformWorkdirs(filepath.Join(t.TempDir(), "nope"), filepath.Join(t.TempDir(), "keep"))
	if err != nil {
		t.Fatalf("missing root should be (0,nil), got %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}
