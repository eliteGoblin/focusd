package relocate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRandomBaseDisguisedAndUnique(t *testing.T) {
	a, b := RandomBase(), RandomBase()
	if !strings.HasPrefix(a, "com.apple.") {
		t.Fatalf("base must look Apple-ish: %s", a)
	}
	if strings.Contains(a, "focusd") {
		t.Fatalf("base must NOT contain 'focusd': %s", a)
	}
	if a == b {
		t.Fatalf("bases must be per-install unique: %s == %s", a, b)
	}
	if n := strings.Count(a, "."); n < 3 {
		t.Fatalf("unexpected base shape: %s", a)
	}
}

func TestHiddenWorkdir(t *testing.T) {
	wd := HiddenWorkdir("/Users/x")
	if !strings.HasPrefix(wd, "/Users/x/Library/Application Support/.") {
		t.Fatalf("workdir not hidden under App Support: %s", wd)
	}
	if strings.Contains(wd, "focusd") {
		t.Fatalf("workdir must not contain 'focusd': %s", wd)
	}
}

func TestRelocateIntoCopiesExecutable(t *testing.T) {
	src := filepath.Join(t.TempDir(), "daemon")
	if err := os.WriteFile(src, []byte("BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "hidden")
	dst, err := RelocateInto(src, dir)
	if err != nil {
		t.Fatalf("RelocateInto: %v", err)
	}
	if filepath.Dir(dst) != dir {
		t.Fatalf("dst not in target dir: %s", dst)
	}
	if strings.Contains(filepath.Base(dst), "focusd") {
		t.Fatalf("relocated name must be disguised: %s", dst)
	}
	b, _ := os.ReadFile(dst)
	if string(b) != "BINARY" {
		t.Fatalf("content not copied: %q", b)
	}
	fi, _ := os.Stat(dst)
	if fi.Mode()&0o100 == 0 {
		t.Fatal("relocated binary must be executable")
	}
}
