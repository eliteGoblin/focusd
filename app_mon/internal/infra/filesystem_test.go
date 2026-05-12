package infra

import (
	"os"
	"path/filepath"
	"testing"
)

// FileSystemManager is the actual file-touching layer used by the enforcer
// on every tick. Bugs here = either "deletes nothing" (protection fails
// silently) or "deletes the wrong thing" (data loss). These tests pin the
// behavior the enforcer relies on.

func TestExpandHome(t *testing.T) {
	const home = "/Users/test"
	fm := NewFileSystemManagerWithHome(home).(*FileSystemManagerImpl)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"tilde-slash prefix", "~/Library/foo", "/Users/test/Library/foo"},
		{"bare tilde", "~", "/Users/test"},
		{"absolute path unchanged", "/Applications/Steam.app", "/Applications/Steam.app"},
		{"relative path unchanged", "./foo", "./foo"},
		{"tilde-in-middle not expanded", "/some/~/path", "/some/~/path"},
		{"empty string", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fm.ExpandHome(tc.in); got != tc.want {
				t.Errorf("ExpandHome(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExists_RealAndAbsent(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "file")
	if err := os.WriteFile(present, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(dir, "no-such-file")

	fm := NewFileSystemManagerWithHome(dir)
	if !fm.Exists(present) {
		t.Errorf("Exists(%q) = false, want true", present)
	}
	if fm.Exists(absent) {
		t.Errorf("Exists(%q) = true, want false", absent)
	}
}

func TestExists_ExpandsHomeBeforeChecking(t *testing.T) {
	// Enforcer calls fm.Exists(path) with the unexpanded path (e.g., "~/Library/Application Support/Steam")
	// before deleting. ExpandHome must apply transparently, otherwise Exists
	// returns false and the path is silently skipped.
	dir := t.TempDir()
	libPath := filepath.Join(dir, "MarkerFile")
	if err := os.WriteFile(libPath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	fm := NewFileSystemManagerWithHome(dir)
	if !fm.Exists("~/MarkerFile") {
		t.Errorf("Exists(\"~/MarkerFile\") = false; ExpandHome wasn't applied")
	}
}

func TestDelete_RemovesFileRecursively(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "victim")
	if err := os.MkdirAll(filepath.Join(target, "nested", "deep"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "nested", "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	fm := NewFileSystemManagerWithHome(dir)
	if err := fm.Delete(target); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("target still exists after Delete: stat err = %v", err)
	}
}

func TestDelete_TildePath(t *testing.T) {
	// Enforcer passes paths like "~/Library/Application Support/Steam".
	// Delete must expand the tilde — otherwise it deletes nothing
	// (or worse, a literal directory called "~" if one happens to exist).
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Library", "App"), 0755); err != nil {
		t.Fatal(err)
	}

	fm := NewFileSystemManagerWithHome(dir)
	if err := fm.Delete("~/Library/App"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Library", "App")); !os.IsNotExist(err) {
		t.Errorf("~ expansion failed: real path still exists")
	}
}

func TestDelete_NonexistentPathIsNoError(t *testing.T) {
	// Enforcer guards with Exists() but Delete itself must also be idempotent —
	// os.RemoveAll returns nil on missing paths, and we rely on that.
	fm := NewFileSystemManagerWithHome(t.TempDir())
	if err := fm.Delete(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Errorf("Delete on missing path returned err: %v", err)
	}
}

func TestDelete_GlobMatchesMultiple(t *testing.T) {
	// Dota 2 policy uses globs like ~/Downloads/*[Dd]ota*.dmg. Delete must
	// expand the glob and remove every match. A regression where we forget
	// the glob branch would leave .dmg installers on disk.
	dir := t.TempDir()
	files := []string{"DotaInstaller.dmg", "dota-other.dmg", "unrelated.txt"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	fm := NewFileSystemManagerWithHome(dir)
	if err := fm.Delete(filepath.Join(dir, "*[Dd]ota*.dmg")); err != nil {
		t.Fatalf("Delete glob: %v", err)
	}

	// Both .dmg files matched and were removed
	for _, name := range []string{"DotaInstaller.dmg", "dota-other.dmg"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still exists after glob delete", name)
		}
	}
	// Unrelated file untouched
	if _, err := os.Stat(filepath.Join(dir, "unrelated.txt")); err != nil {
		t.Errorf("unrelated file was deleted: %v", err)
	}
}

func TestDelete_GlobNoMatchesIsNoError(t *testing.T) {
	dir := t.TempDir()
	fm := NewFileSystemManagerWithHome(dir)
	// Glob that matches nothing — filepath.Glob returns empty slice + nil err.
	if err := fm.Delete(filepath.Join(dir, "*.nonexistent")); err != nil {
		t.Errorf("Delete with no glob matches: %v", err)
	}
}
