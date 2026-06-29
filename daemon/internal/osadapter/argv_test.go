package osadapter

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWorkdirFromBinary covers the FEATURE 14 / ADR-0018 workdir recovery and
// its edge guards: filepath.Dir yields "." for a relative path and "/" for a
// root-level binary — both non-empty, which would otherwise short-circuit the
// caller's fallback (deriveMeshWorkdir → defaultWorkdir) into a nonsensical
// workdir. The guard collapses those to "" so the caller falls back cleanly.
func TestWorkdirFromBinary(t *testing.T) {
	cases := []struct {
		name string
		self string
		want string
	}{
		{"absolute nested", "/foo/bar", "/foo"},
		{"root-level binary", "/foo", ""},
		{"root self", "/", ""},
		{"relative path", "bar/baz", ""},
		{"bare relative", "baz", ""},
		{"dot relative", ".", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := WorkdirFromBinary(c.self); got != c.want {
				t.Fatalf("WorkdirFromBinary(%q) = %q, want %q", c.self, got, c.want)
			}
		})
	}
}

// TestHasMeshFlag covers the FEATURE 17 generation-membership corroboration:
// a worker argv (carries --mesh) is recognised; an ensure argv (`ensure`, no
// --mesh) and an empty argv are not.
func TestHasMeshFlag(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want bool
	}{
		{"worker run --mesh", []string{"/bin/x", "run", "--r", "a", "--mesh"}, true},
		{"ensure (no mesh)", []string{"/bin/x", "ensure"}, false},
		{"unrelated", []string{"/bin/x", "--github", "o/r"}, false},
		{"empty", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasMeshFlag(c.argv); got != c.want {
				t.Fatalf("hasMeshFlag(%v) = %v, want %v", c.argv, got, c.want)
			}
		})
	}
}

// TestSafeToRemoveWorkdir pins the os.RemoveAll guard for generation cleanup
// (FEATURE 17, Item 3): only an absolute path STRICTLY under the support root,
// that is neither the keep workdir nor an ancestor of it, may be removed. The
// guard now resolves symlinks on BOTH dir and supportRoot, so real on-disk
// dirs are required — t.TempDir() + os.MkdirAll give us those.
func TestSafeToRemoveWorkdir(t *testing.T) {
	mkdir := func(p string) string {
		t.Helper()
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}

	root := t.TempDir()
	// keep lives one level deep so we can also exercise an ancestor-of-keep dir.
	keepParent := mkdir(filepath.Join(root, ".keepgen.aaaa"))
	keep := mkdir(filepath.Join(keepParent, "inner"))
	oldGen := mkdir(filepath.Join(root, ".oldgen.bbbb"))
	nestedOld := mkdir(filepath.Join(oldGen, "bin"))
	outside := t.TempDir() // a real dir entirely outside root

	cases := []struct {
		name        string
		dir         string
		supportRoot string
		keep        string
		want        bool
	}{
		{"valid old generation", oldGen, root, keep, true},
		{"valid nested old generation", nestedOld, root, keep, true},
		{"empty dir", "", root, keep, false},
		{"relative dir", "oldgen", root, keep, false},
		{"relative supportRoot", oldGen, "Library/Application Support", keep, false},
		{"non-existent dir under root", filepath.Join(root, ".ghost"), root, keep, false},
		{"the support root itself", root, root, keep, false},
		{"outside the support root", outside, root, keep, false},
		{"the keep workdir", keep, root, keep, false},
		{"ancestor of keep (under root)", keepParent, root, keep, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := safeToRemoveWorkdir(c.dir, c.supportRoot, c.keep); got != c.want {
				t.Fatalf("safeToRemoveWorkdir(%q, %q, %q) = %v, want %v",
					c.dir, c.supportRoot, c.keep, got, c.want)
			}
		})
	}

	// A symlinked INTERMEDIATE component that escapes the root must be refused:
	// <root>/link -> <outside>, so <root>/link/evil is lexically under root but
	// RemoveAll would follow the link and delete OUTSIDE root.
	t.Run("symlinked intermediate escapes root", func(t *testing.T) {
		linkRoot := t.TempDir()
		escapeTarget := t.TempDir()
		mkdir(filepath.Join(escapeTarget, "evil"))
		if err := os.Symlink(escapeTarget, filepath.Join(linkRoot, "link")); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(linkRoot, "link", "evil") // resolves outside linkRoot
		if safeToRemoveWorkdir(dir, linkRoot, "") {
			t.Fatal("a dir whose intermediate symlink escapes the root must be refused")
		}
	})

	// Empty keepWorkdir must not weaken the under-root guard (happy path).
	if !safeToRemoveWorkdir(oldGen, root, "") {
		t.Fatal("a valid under-root dir must be removable even with no keep workdir")
	}
	if safeToRemoveWorkdir(outside, root, "") {
		t.Fatal("a path outside the root must never be removable")
	}
}
