package infra

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFakeBinary writes some bytes to a temp path with 0755 perms.
func writeFakeBinary(t *testing.T, dir, name string, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return p
}

func TestRelocator_Relocate_BasenameIsObfuscated(t *testing.T) {
	home := t.TempDir()
	src := writeFakeBinary(t, t.TempDir(), "appmon", "stub")

	r := NewRelocator(home)
	dst, err := r.Relocate(src)
	if err != nil {
		t.Fatalf("Relocate: %v", err)
	}

	base := filepath.Base(dst)
	if base == "appmon" {
		t.Fatalf("relocated basename should not be 'appmon', got %q", base)
	}
	if strings.Contains(base, "appmon") {
		t.Fatalf("relocated basename should not contain 'appmon', got %q", base)
	}
	if !strings.HasPrefix(base, "com.apple.") {
		t.Fatalf("expected system-looking prefix, got %q", base)
	}

	// Path should not contain "appmon" anywhere — defeats `pkill -f appmon`.
	if strings.Contains(dst, "appmon") {
		t.Fatalf("relocated path should not contain 'appmon', got %q", dst)
	}
}

func TestRelocator_Relocate_ContentMatches(t *testing.T) {
	home := t.TempDir()
	src := writeFakeBinary(t, t.TempDir(), "appmon", "payload-xyz")

	r := NewRelocator(home)
	dst, err := r.Relocate(src)
	if err != nil {
		t.Fatalf("Relocate: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read relocated: %v", err)
	}
	if string(got) != "payload-xyz" {
		t.Fatalf("content mismatch: got %q", string(got))
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat relocated: %v", err)
	}
	if info.Mode().Perm()&0100 == 0 {
		t.Fatalf("relocated file should be executable, mode=%v", info.Mode())
	}
}

func TestRelocator_Relocate_FreshBasenamePerCall(t *testing.T) {
	home := t.TempDir()
	src := writeFakeBinary(t, t.TempDir(), "appmon", "data")
	r := NewRelocator(home)

	seen := map[string]struct{}{}
	for i := 0; i < 6; i++ {
		dst, err := r.Relocate(src)
		if err != nil {
			t.Fatalf("Relocate %d: %v", i, err)
		}
		if _, dup := seen[dst]; dup {
			t.Fatalf("got duplicate relocated path on iteration %d: %s", i, dst)
		}
		seen[dst] = struct{}{}
	}
}

func TestRelocator_CleanStale_RespectsKeepAndMinAge(t *testing.T) {
	home := t.TempDir()
	r := NewRelocator(home)

	// Create three relocated files, manually backdate two of them.
	srcDir := t.TempDir()
	src := writeFakeBinary(t, srcDir, "appmon", "data")

	a, err := r.Relocate(src)
	if err != nil {
		t.Fatalf("Relocate a: %v", err)
	}
	b, err := r.Relocate(src)
	if err != nil {
		t.Fatalf("Relocate b: %v", err)
	}
	c, err := r.Relocate(src)
	if err != nil {
		t.Fatalf("Relocate c: %v", err)
	}

	old := time.Now().Add(-10 * time.Minute)
	for _, p := range []string{a, b, c} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("Chtimes %s: %v", p, err)
		}
	}

	if err := r.CleanStale([]string{a}, 5*time.Minute); err != nil {
		t.Fatalf("CleanStale: %v", err)
	}

	if _, err := os.Stat(a); err != nil {
		t.Fatalf("kept file should still exist: %v", err)
	}
	if _, err := os.Stat(b); !os.IsNotExist(err) {
		t.Fatalf("expected b to be removed, stat err = %v", err)
	}
	if _, err := os.Stat(c); !os.IsNotExist(err) {
		t.Fatalf("expected c to be removed, stat err = %v", err)
	}
}

func TestRelocator_CleanStale_SkipsFreshEntries(t *testing.T) {
	home := t.TempDir()
	r := NewRelocator(home)
	src := writeFakeBinary(t, t.TempDir(), "appmon", "data")

	fresh, err := r.Relocate(src)
	if err != nil {
		t.Fatalf("Relocate: %v", err)
	}

	// minAge=1h, file just created → must not be deleted.
	if err := r.CleanStale(nil, time.Hour); err != nil {
		t.Fatalf("CleanStale: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh file was unexpectedly removed: %v", err)
	}
}

func TestRelocator_CleanStale_MissingDirIsNotError(t *testing.T) {
	r := NewRelocator(t.TempDir())
	if err := r.CleanStale(nil, time.Minute); err != nil {
		t.Fatalf("CleanStale on missing dir: %v", err)
	}
}

func TestRelocator_FindProcessesUsingDir_SeesOurOwnExecution(t *testing.T) {
	// `go test` itself runs from a binary outside our relocator dir, so a
	// dir we just created should report no PIDs.
	r := NewRelocator(t.TempDir())
	if err := os.MkdirAll(r.dir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	pids, err := r.FindProcessesUsingDir()
	if err != nil {
		t.Fatalf("FindProcessesUsingDir: %v", err)
	}
	if len(pids) != 0 {
		t.Fatalf("expected no PIDs in empty relocator dir, got %v", pids)
	}
}
