//go:build darwin

package osadapter

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/platdir"
)

// FEATURE 26 destructive-safety proof. Both generation sweeps now scan ALL
// children of the support root (the leading-dot pre-filter is gone), so they stat
// INSIDE real app folders. This table test plants a rich set of real-app
// LOOKALIKES alongside genuine focusd dirs and asserts the ONLY things ever
// removed are the genuine focusd dirs — a positive content-magic match. Every
// real-app lookalike MUST survive, including ones deliberately baited with a
// state.db, a 16-byte file, or a sentinel-looking basename.

// writeFiles scaffolds a directory with the given basename→content files.
func writeFiles(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// realAppLookalikes returns a set of directories that resemble ordinary
// ~/Library/Application Support entries — none is a focusd dir, so NONE may ever
// be deleted by either sweep. Includes adversarial bait: a state.db, an
// exactly-16-byte file of random bytes, and a file whose basename collides with a
// focusd sentinel-pool name but whose content is not the magic.
func realAppLookalikes(t *testing.T, root string) map[string]string {
	t.Helper()
	sixteen := make([]byte, 16) // exactly magicLen, but random content (not the magic)
	_, _ = rand.Read(sixteen)

	dirs := map[string]string{}
	dirs["Google"] = writeFiles(t, filepath.Join(root, "Google"), map[string]string{
		"Chrome/Default/Cookies": "SQLITE",
		"state.db":               "REAL GOOGLE STATE", // bait: OLD heuristic keyed on this
	})
	dirs["Spotify"] = writeFiles(t, filepath.Join(root, "Spotify"), map[string]string{
		"PersistentCache/x": "blob",
		"state.db":          "REAL SPOTIFY STATE",
	})
	dirs["com.apple.metadata"] = writeFiles(t, filepath.Join(root, "com.apple.metadata"), map[string]string{
		"index.plist": "<plist/>",
	})
	dirs["Firefox"] = writeFiles(t, filepath.Join(root, "Firefox"), map[string]string{
		"Profiles/abc/prefs.js": "user_pref();",
	})
	// A 16-byte file of RANDOM bytes: proves the exact-size gate alone does not
	// trigger a match — only the un-masked-equals-magic content does.
	d := filepath.Join(root, "Fantastical")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, ".cache.state"), sixteen, 0o644); err != nil {
		t.Fatal(err)
	}
	dirs["Fantastical"] = d
	// A file whose basename matches a focusd sentinel-pool name but whose content
	// is NOT the magic (e.g. a real macOS metadata file).
	dirs["Bear"] = writeFiles(t, filepath.Join(root, "Bear"), map[string]string{
		".DocumentRevisions.plist": "<real apple metadata, not our magic>",
	})
	// A dir with the LEGACY platform sentinel basename but NO state.db → the
	// two-signal legacy recogniser must NOT match (needs both).
	dirs["LegacyHalf"] = writeFiles(t, filepath.Join(root, "com.vendor.helper"), map[string]string{
		".com.apple.metadata.pwd.plist": "", // legacy sentinel present…
		// …but no state.db → not recognised as a legacy platform-workdir.
	})
	// A dir with state.db but NO legacy sentinel → also must NOT match.
	dirs["StateOnly"] = writeFiles(t, filepath.Join(root, "SomeApp"), map[string]string{
		"state.db": "not ours",
	})
	return dirs
}

func assertAllSurvive(t *testing.T, label string, dirs map[string]string) {
	t.Helper()
	for name, dir := range dirs {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s: real-app lookalike %q was DELETED (stat err = %v) — DESTRUCTIVE SAFETY VIOLATION", label, name, err)
		}
	}
}

// TestSweepStalePlatformWorkdirs_NeverDeletesRealApps: with a forest of real-app
// lookalikes present, SweepStalePlatformWorkdirs reaps ONLY the planted stale
// platform-workdirs (content magic + the legacy two-signal marker) and NEVER a
// real folder.
func TestSweepStalePlatformWorkdirs_NeverDeletesRealApps(t *testing.T) {
	root := t.TempDir()
	real := realAppLookalikes(t, root)

	// Genuine focusd platform-workdirs.
	keep := filepath.Join(root, "LiveEngineStore")
	if err := os.MkdirAll(keep, 0o700); err != nil {
		t.Fatal(err)
	}
	platdir.MarkPlatformWorkdir(keep)

	stale := filepath.Join(root, "OldEngineStore")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	platdir.MarkPlatformWorkdir(stale)

	// A genuine LEGACY platform-workdir (both signals present) → must be reaped.
	legacy := writeFiles(t, filepath.Join(root, "com.legacy.helper"), map[string]string{
		".com.apple.metadata.pwd.plist": "",
		"state.db":                      "old engine state",
	})

	removed, err := SweepStalePlatformWorkdirs(root, keep)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2 (stale new + legacy); a wrong count means a real app was hit or a stale one missed", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale platform-workdir must be reaped, stat err = %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy platform-workdir (2-signal) must be reaped, stat err = %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("keep must survive: %v", err)
	}
	assertAllSurvive(t, "stale-platform-sweep", real)
}

// TestSweepOrphanWorkdirs_NeverDeletesRealApps: with the same forest present,
// SweepOrphanWorkdirs reaps ONLY the planted orphan daemon-home (content magic)
// and NEVER a real folder — in particular the Google/ and SomeApp/ dirs that hold
// a state.db (the removed heuristic's bait) must survive.
func TestSweepOrphanWorkdirs_NeverDeletesRealApps(t *testing.T) {
	root := t.TempDir()
	real := realAppLookalikes(t, root)

	keep := filepath.Join(root, "KeepVendorAgent")
	if err := os.MkdirAll(keep, 0o700); err != nil {
		t.Fatal(err)
	}
	platdir.MarkDaemonHome(keep)

	orphan := filepath.Join(root, "OrphanVendorAgent")
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	platdir.MarkDaemonHome(orphan)

	// A genuine platform-workdir is present too: it must NOT be swept by the
	// daemon-home sweep (different magic) — SweepStalePlatformWorkdirs owns it.
	pw := filepath.Join(root, "SomeEngineStore")
	if err := os.MkdirAll(pw, 0o700); err != nil {
		t.Fatal(err)
	}
	platdir.MarkPlatformWorkdir(pw)

	removed, err := SweepOrphanWorkdirs(root, keep)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (only the orphan daemon-home)", removed)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan daemon-home must be reaped, stat err = %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("keep daemon-home must survive: %v", err)
	}
	if _, err := os.Stat(pw); err != nil {
		t.Fatalf("a platform-workdir must NOT be swept by the daemon-home sweep: %v", err)
	}
	assertAllSurvive(t, "orphan-daemon-home-sweep", real)
}

// TestWatchdogRemoveCopyDirNeverDeletesRealApp: the REAL copyFS.removeCopyDir
// deletes a dir only on a positive watchdog-copy content match — so a blended
// name collision or a tampered cron path pointing at a real app folder can never
// wipe it. A marked watchdog-copy dir IS removed.
func TestWatchdogRemoveCopyDirNeverDeletesRealApp(t *testing.T) {
	root := t.TempDir()

	realApp := filepath.Join(root, "Google")
	if err := os.MkdirAll(realApp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realApp, "Cookies"), []byte("real data"), 0o644); err != nil {
		t.Fatal(err)
	}

	fs := realCopyFS{}
	if err := fs.removeCopyDir(realApp); err != nil {
		t.Fatalf("removeCopyDir: %v", err)
	}
	if _, err := os.Stat(realApp); err != nil {
		t.Fatalf("DESTRUCTIVE SAFETY VIOLATION: real app folder deleted by removeCopyDir: %v", err)
	}

	wd := filepath.Join(root, "com.vendor.helper")
	if err := os.MkdirAll(wd, 0o700); err != nil {
		t.Fatal(err)
	}
	platdir.MarkWatchdogCopy(wd)
	if err := fs.removeCopyDir(wd); err != nil {
		t.Fatalf("removeCopyDir(marked): %v", err)
	}
	if _, err := os.Stat(wd); !os.IsNotExist(err) {
		t.Fatalf("marked watchdog-copy dir must be removed, stat err = %v", err)
	}
}
