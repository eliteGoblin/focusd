package uninstaller

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetect_AbsentIsCheap(t *testing.T) {
	r := &Reconciler{AppPath: filepath.Join(t.TempDir(), "does-not-exist.app")}
	if r.Detect() {
		t.Fatal("expected Detect() false when AppPath missing")
	}
	o := r.Reconcile()
	if o.Detected || len(o.Removed) != 0 {
		t.Fatalf("unexpected outcome on absent: %+v", o)
	}
}

func TestReconcile_RemovesEverything(t *testing.T) {
	root := t.TempDir()
	// Fake "/Applications/Steam.app"
	app := filepath.Join(root, "Apps", "Steam.app")
	os.MkdirAll(app, 0o755)
	os.WriteFile(filepath.Join(app, "Info.plist"), []byte("x"), 0o644)
	// Fake users dir with two homes
	usersDir := filepath.Join(root, "Users")
	for _, u := range []string{"alice", "bob", ".hidden", "Shared"} {
		os.MkdirAll(filepath.Join(usersDir, u, "Library", "Application Support"), 0o755)
	}
	// Drop the Steam appdata under each REAL user (alice + bob)
	for _, u := range []string{"alice", "bob"} {
		appdata := filepath.Join(usersDir, u, "Library", "Application Support", "Steam")
		os.MkdirAll(filepath.Join(appdata, "steamapps", "common", "dota 2 beta"), 0o755)
		os.WriteFile(filepath.Join(appdata, "config.vdf"), []byte("x"), 0o644)
	}
	// Plus a Steam LaunchAgent for alice
	os.MkdirAll(filepath.Join(usersDir, "alice", "Library", "LaunchAgents"), 0o755)
	os.WriteFile(
		filepath.Join(usersDir, "alice", "Library", "LaunchAgents", "com.valvesoftware.steamclean.plist"),
		[]byte("<?xml ?>"), 0o644)

	r := &Reconciler{
		AppPath:  app,
		UsersDir: usersDir,
		System:   []systemTarget{{Path: app, What: "test Steam.app"}},
	}

	o := r.Reconcile()
	if !o.Detected {
		t.Fatal("expected Detected=true")
	}
	if _, err := os.Stat(app); !os.IsNotExist(err) {
		t.Fatalf("Steam.app must be removed: %v", err)
	}
	for _, u := range []string{"alice", "bob"} {
		appdata := filepath.Join(usersDir, u, "Library", "Application Support", "Steam")
		if _, err := os.Stat(appdata); !os.IsNotExist(err) {
			t.Fatalf("%s appdata must be removed", u)
		}
	}
	aliceAgent := filepath.Join(usersDir, "alice", "Library", "LaunchAgents", "com.valvesoftware.steamclean.plist")
	if _, err := os.Stat(aliceAgent); !os.IsNotExist(err) {
		t.Fatal("alice's Steam LaunchAgent must be removed")
	}
	// Hidden + Shared dirs were NOT iterated (we'd have failed to remove
	// nonexistent paths under them, that's fine — just sanity-check we
	// didn't crash). At least one removal recorded:
	if len(o.Removed) < 3 {
		t.Fatalf("expected ≥3 removals, got %d: %+v", len(o.Removed), o.Removed)
	}
}

func TestReconcile_IdempotentAfterRemoval(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "Steam.app")
	os.MkdirAll(app, 0o755)
	r := &Reconciler{
		AppPath:  app,
		UsersDir: filepath.Join(root, "Users"),
		System:   []systemTarget{{Path: app, What: "test"}},
	}
	os.MkdirAll(filepath.Join(root, "Users", "alice"), 0o755)
	// First pass: removes
	o1 := r.Reconcile()
	if !o1.Detected || len(o1.Removed) == 0 {
		t.Fatalf("first pass should remove: %+v", o1)
	}
	// Second pass: noop (Steam.app gone => Detect=false)
	o2 := r.Reconcile()
	if o2.Detected {
		t.Fatalf("second pass should be noop, got: %+v", o2)
	}
}
