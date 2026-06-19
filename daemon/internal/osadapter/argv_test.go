package osadapter

import "testing"

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
