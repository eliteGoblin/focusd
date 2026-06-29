package osadapter

import (
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
// that is neither the keep workdir nor an ancestor of it, may be removed.
func TestSafeToRemoveWorkdir(t *testing.T) {
	root := "/Library/Application Support"
	keep := filepath.Join(root, ".keepgen.aaaa")
	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"valid old generation", filepath.Join(root, ".oldgen.bbbb"), true},
		{"valid nested old generation", filepath.Join(root, ".oldgen.bbbb", "bin"), true},
		{"empty dir", "", false},
		{"relative dir", "oldgen", false},
		{"the support root itself", root, false},
		{"outside the support root", "/Library/LaunchDaemons/x", false},
		{"escape via traversal sibling", "/Library/Application SupportXX/y", false},
		{"the keep workdir", keep, false},
		{"ancestor of keep (== root parent)", "/Library", false},
		{"ancestor of keep (root)", root, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := safeToRemoveWorkdir(c.dir, root, keep); got != c.want {
				t.Fatalf("safeToRemoveWorkdir(%q) = %v, want %v", c.dir, got, c.want)
			}
		})
	}

	// Empty keepWorkdir must not weaken the under-root guard.
	if !safeToRemoveWorkdir(filepath.Join(root, ".g.cccc"), root, "") {
		t.Fatal("a valid under-root dir must be removable even with no keep workdir")
	}
	if safeToRemoveWorkdir("/etc", root, "") {
		t.Fatal("a path outside the root must never be removable")
	}
}
