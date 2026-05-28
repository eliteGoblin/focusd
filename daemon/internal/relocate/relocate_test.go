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

func TestRandomBinaryNameShapeAndUnique(t *testing.T) {
	a, b := RandomBinaryName(), RandomBinaryName()
	if strings.Contains(a, "focusd") || strings.Contains(a, "daemon") {
		t.Fatalf("disguised name leaked project string: %s", a)
	}
	// Shape: <prefix>.<suffix>.<4hex> → 3 dots minimum (prefixes are
	// dotted Apple-style themselves), suffix is alpha, 4-hex tail.
	if n := strings.Count(a, "."); n < 3 {
		t.Fatalf("name should have at least 3 dots (apple-style prefix.suffix.hex): %s", a)
	}
	parts := strings.Split(a, ".")
	tail := parts[len(parts)-1]
	if len(tail) != 8 { // 4 bytes hex-encoded = 8 chars
		t.Fatalf("tail must be 8 hex chars (4 random bytes), got %q in %s", tail, a)
	}
	for _, c := range tail {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("tail %q must be lowercase hex: %s", tail, a)
		}
	}
	if a == b {
		t.Fatalf("names must be per-call unique: %s == %s", a, b)
	}
}

func TestHiddenWorkdir(t *testing.T) {
	wd := HiddenWorkdir("/Users/x/Library/Application Support")
	if !strings.HasPrefix(wd, "/Users/x/Library/Application Support/.") {
		t.Fatalf("workdir not hidden under support root: %s", wd)
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
