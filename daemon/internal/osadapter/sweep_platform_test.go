package osadapter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/platdir"
)

// mkPlatformWorkdir creates a hidden-dot dir under root and marks it as a
// platform-workdir (the sentinel SweepStalePlatformWorkdirs keys on).
func mkPlatformWorkdir(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(platdir.SentinelPath(dir), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestSweepStalePlatformWorkdirs: stale sentinel-marked platform-workdirs are
// removed; the live one (keep) survives; a daemon-home (NO sentinel) is never a
// candidate even though it is a hidden-dot sibling.
func TestSweepStalePlatformWorkdirs(t *testing.T) {
	root := t.TempDir()

	keep := mkPlatformWorkdir(t, root, ".live.pw")
	stale := mkPlatformWorkdir(t, root, ".stale.pw")

	// A daemon-home: hidden-dot, has version.json + a pointer, but NO sentinel.
	daemonHome := filepath.Join(root, ".daemon.home")
	if err := os.MkdirAll(daemonHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(daemonHome, "version.json"), []byte(`{"desired":"v1"}`), 0o600); err != nil {
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
		t.Fatalf("daemon-home (no sentinel) must NEVER be swept: %v", err)
	}
}

// TestSweepStalePlatformWorkdirsNoRoot: a missing support root is not an error.
func TestSweepStalePlatformWorkdirsNoRoot(t *testing.T) {
	removed, err := SweepStalePlatformWorkdirs(filepath.Join(t.TempDir(), "nope"), filepath.Join(t.TempDir(), ".keep"))
	if err != nil {
		t.Fatalf("missing root should be (0,nil), got %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}
