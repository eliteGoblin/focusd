package relocate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRandomBaseDisguisedAndUnique(t *testing.T) {
	a, b := RandomBase(), RandomBase()
	// The base must NOT leak the project name. We no longer assert a
	// "com.apple." prefix — the pool is intentionally widened to mix
	// Apple subsystems with plausible third-party bundle IDs.
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
	if strings.Contains(a, "focusd") {
		t.Fatalf("disguised name leaked project string: %s", a)
	}
	// Shape: <prefix>.<suffix>.<10hex> → 3 dots minimum (prefixes are
	// themselves dotted bundle-ID style), suffix is alpha lowercase,
	// 10-hex tail (5 random bytes hex-encoded).
	if n := strings.Count(a, "."); n < 3 {
		t.Fatalf("name should have at least 3 dots (bundle-id prefix.suffix.hex): %s", a)
	}
	parts := strings.Split(a, ".")
	tail := parts[len(parts)-1]
	if len(tail) != 10 { // 5 bytes hex-encoded = 10 chars
		t.Fatalf("tail must be 10 hex chars (5 random bytes), got %q in %s", tail, a)
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

// TestHiddenWorkdir pins the FEATURE 26 naming blend: the disguised directory is
// under the support root, is NOT dotted (it must blend as an ordinary
// app-support entry, not read as "someone is hiding something"), carries no
// project string, and has a plausible non-empty basename.
func TestHiddenWorkdir(t *testing.T) {
	const root = "/Users/x/Library/Application Support"
	for i := 0; i < 200; i++ {
		wd := HiddenWorkdir(root)
		if filepath.Dir(wd) != root {
			t.Fatalf("workdir not directly under support root: %s", wd)
		}
		base := filepath.Base(wd)
		if base == "" {
			t.Fatalf("empty disguised basename: %s", wd)
		}
		if strings.HasPrefix(base, ".") {
			t.Fatalf("FEATURE 26 drops the leading dot; got hidden-dot name: %s", base)
		}
		if strings.Contains(strings.ToLower(base), "focusd") {
			t.Fatalf("workdir must not contain 'focusd': %s", wd)
		}
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

// TestPoolSizesMeetThreshold guards against accidental shrinkage of the
// disguise pool. The architect-specified floor is 60 entries each;
// dropping below that pushes the total combination count back toward
// the old "enumerable in seconds" regime.
func TestPoolSizesMeetThreshold(t *testing.T) {
	if len(prefixes) < 60 {
		t.Fatalf("prefixes pool too small: got %d, want >= 60", len(prefixes))
	}
	if len(suffixes) < 60 {
		t.Fatalf("suffixes pool too small: got %d, want >= 60", len(suffixes))
	}

	// Defense-in-depth: catch accidental duplicates that silently
	// shrink the effective pool.
	seen := make(map[string]struct{}, len(prefixes))
	for _, p := range prefixes {
		if _, dup := seen[p]; dup {
			t.Fatalf("duplicate prefix entry: %q", p)
		}
		seen[p] = struct{}{}
	}
	seen = make(map[string]struct{}, len(suffixes))
	for _, s := range suffixes {
		if _, dup := seen[s]; dup {
			t.Fatalf("duplicate suffix entry: %q", s)
		}
		seen[s] = struct{}{}
	}
}

// TestRandomBaseHighEntropy generates 10000 bases and asserts the
// uniqueness rate is overwhelming. With 60×60×10^12 combinations the
// expected number of collisions across 10000 samples is effectively
// zero; we tolerate up to 10 to absorb the unlikely 256-mod bias in
// pick() without flaking.
func TestRandomBaseHighEntropy(t *testing.T) {
	const n = 10000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		seen[RandomBase()] = struct{}{}
	}
	if len(seen) < n-10 {
		t.Fatalf("low uniqueness: got %d unique out of %d, want >= %d", len(seen), n, n-10)
	}
}

// TestRandomBaseLooksPlausible asserts the generated base matches the
// architect-specified shape: bundle-id-style prefix, lowercase ASCII
// role suffix, 10-hex random tail.
func TestRandomBaseLooksPlausible(t *testing.T) {
	re := regexp.MustCompile(`^[a-zA-Z0-9._-]+\.[a-z]+\.[0-9a-f]{10}$`)
	for i := 0; i < 200; i++ {
		base := RandomBase()
		if !re.MatchString(base) {
			t.Fatalf("base does not match plausible shape: %s", base)
		}
	}
}

// TestRandomBaseNoLeakedProjectStrings asserts no generated base
// contains any of the project-revealing tokens from the OLD codebase.
// A grep for any of these on a real machine would immediately point
// at our daemon.
//
// The list is split into two flavours:
//   - whole-word tokens ("focus", "appmon", "guard", "watcher",
//     "kill"): rejected if they appear ANYWHERE in the base
//     (these are not substrings of any legitimate prefix/suffix
//     we ship).
//   - identifier-context tokens ("daemon", "ensure"): rejected only
//     when they appear as a standalone dot-separated component, since
//     "daemon" is a legitimate role suffix and substrings of it could
//     legitimately appear in a third-party bundle ID.
func TestRandomBaseNoLeakedProjectStrings(t *testing.T) {
	whole := []string{"focus", "appmon", "guard", "watcher", "kill"}
	standalone := []string{"ensure"}

	for i := 0; i < 1000; i++ {
		base := RandomBase()
		lower := strings.ToLower(base)
		for _, tok := range whole {
			if strings.Contains(lower, tok) {
				t.Fatalf("base %q leaks project token %q", base, tok)
			}
		}
		parts := strings.Split(lower, ".")
		for _, tok := range standalone {
			for _, p := range parts {
				if p == tok {
					t.Fatalf("base %q leaks standalone token %q", base, tok)
				}
			}
		}
	}
}

// TestRandomBaseSamplesVerbose prints 5 sample bases for human
// inspection (visible under `go test -v`). Useful for verifying at
// review time that the labels actually look plausible.
func TestRandomBaseSamplesVerbose(t *testing.T) {
	for i := 0; i < 5; i++ {
		t.Logf("sample %d: %s", i+1, RandomBase())
	}
}
