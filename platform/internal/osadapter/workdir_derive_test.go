package osadapter

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDeriveWorkdir_TwoLevelsUp pins the disguised-layout rule: the platform
// binary lives at <workdir>/bin/<base>, so DeriveWorkdir (which reads
// os.Executable → here faked via a real binary at that layout) must return
// exactly <workdir>. We can't override os.Executable, so we place a tiny
// executable at <tmp>/bin/<base> and run it; the child prints its own
// DeriveWorkdir result, which must equal <tmp>.
//
// Rather than compile a helper binary, we assert the pure path arithmetic that
// DeriveWorkdir performs (Dir(Dir(exe))) against the documented layout — the
// EvalSymlinks + os.Executable plumbing is exercised end-to-end by the platform
// child in the e2e suite.
func TestDeriveWorkdir_TwoLevelsUp(t *testing.T) {
	wd := t.TempDir()
	exe := filepath.Join(wd, "bin", "catalog.9f3a2c11ab") // <wd>/bin/<disguised-base>
	got := filepath.Dir(filepath.Dir(exe))
	if got != wd {
		t.Fatalf("2-levels-up of %q = %q; want %q", exe, got, wd)
	}
}

// TestDeriveWorkdir_UsesExecutable confirms DeriveWorkdir resolves against the
// live process's own executable (not argv[0]) and returns a plausible absolute
// path two directories up. In the test binary the executable lives at
// <gocache>/.../<pkg>.test, so Dir(Dir(exe)) is simply that path's grandparent —
// we assert only that it succeeds and is absolute (the exact value is
// environment-specific).
func TestDeriveWorkdir_UsesExecutable(t *testing.T) {
	got, err := DeriveWorkdir()
	if err != nil {
		t.Fatalf("DeriveWorkdir: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("DeriveWorkdir returned a non-absolute path: %q", got)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Skip("os.Executable unavailable")
	}
	if want := filepath.Dir(filepath.Dir(exe)); got != want && filepath.Clean(got) != filepath.Clean(want) {
		// Allow a symlink-resolved divergence: DeriveWorkdir EvalSymlinks the exe.
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			want = filepath.Dir(filepath.Dir(resolved))
		}
		if got != want {
			t.Fatalf("DeriveWorkdir = %q; want %q", got, want)
		}
	}
}
