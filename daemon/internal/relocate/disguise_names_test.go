package relocate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDisguisedDirNameShapes: names blend as ordinary app-support entries — no
// leading dot, no 'focusd', non-empty, and (statistically over many draws) the
// ensemble produces more than one distinct SHAPE so no single glob spans them.
func TestDisguisedDirNameShapes(t *testing.T) {
	dotted, camel, plain := 0, 0, 0
	for i := 0; i < 2000; i++ {
		n := disguisedDirName()
		if n == "" {
			t.Fatal("empty disguised name")
		}
		if strings.HasPrefix(n, ".") {
			t.Fatalf("name must not be hidden-dot (FEATURE 26 drops the dot): %q", n)
		}
		if strings.Contains(strings.ToLower(n), "focusd") {
			t.Fatalf("name leaks project string: %q", n)
		}
		if strings.ContainsAny(n, "/ \t\n") {
			t.Fatalf("name has a path/space char (unsafe basename): %q", n)
		}
		switch {
		case strings.Contains(n, "."):
			dotted++ // reverse-DNS bundle id shape
		case n != strings.ToLower(n):
			camel++ // has an uppercase → CamelCase / vendor / app word
		default:
			plain++
		}
	}
	// All three broad families should appear across 2000 draws (each shape ~25%).
	if dotted == 0 || camel == 0 {
		t.Fatalf("ensemble not diverse: dotted=%d camel=%d plain=%d", dotted, camel, plain)
	}
}

// TestFreshHiddenDirIsExclusiveNeverAdopts: FreshHiddenDir never returns a
// pre-existing directory. We pre-create EVERY possible plain-word and bare-vendor
// name so the only way to succeed is a shape that lands on a fresh name — proving
// os.Mkdir (not MkdirAll) rejects collisions and re-rolls.
func TestFreshHiddenDirIsExclusiveNeverAdopts(t *testing.T) {
	root := t.TempDir()
	// Pre-create a real folder that a disguised name could collide with.
	realApp := filepath.Join(root, "Google")
	if err := os.MkdirAll(realApp, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(realApp, "IMPORTANT")
	if err := os.WriteFile(marker, []byte("do not touch"), 0o644); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for i := 0; i < 300; i++ {
		dir, err := FreshHiddenDir(root)
		if err != nil {
			t.Fatalf("FreshHiddenDir: %v", err)
		}
		if seen[dir] {
			t.Fatalf("FreshHiddenDir returned a duplicate path %q (adopted an existing dir)", dir)
		}
		seen[dir] = true
		if dir == realApp {
			t.Fatal("FreshHiddenDir must NEVER return a pre-existing real folder")
		}
	}
	// The pre-existing real folder + its content must be untouched.
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("real folder content was disturbed: %v", err)
	}
}

// TestMarkerBasenameDistinctStableSaltVaried: for one salt every purpose maps to a
// DISTINCT basename (no marker overwrites another) and the mapping is stable;
// different salts vary the assignment; empty salt yields "".
func TestMarkerBasenameDistinctStableSaltVaried(t *testing.T) {
	const salt = "0123456789abcdef0123456789abcdef"

	seen := map[string]string{}
	for _, p := range markerPurposes {
		b := MarkerBasename(salt, p)
		if b == "" {
			t.Fatalf("purpose %q gave empty basename for a non-empty salt", p)
		}
		if b != MarkerBasename(salt, p) {
			t.Fatalf("purpose %q basename not stable", p)
		}
		if other, dup := seen[b]; dup {
			t.Fatalf("purposes %q and %q collide on basename %q", p, other, b)
		}
		seen[b] = p
	}

	if MarkerBasename("", "roster") != "" {
		t.Fatal("empty salt must yield empty basename (legacy fallback)")
	}

	// A different salt should (very likely) permute the assignment.
	const salt2 = "ffffffffffffffffffffffffffffffff"
	diff := false
	for _, p := range markerPurposes {
		if MarkerBasename(salt, p) != MarkerBasename(salt2, p) {
			diff = true
			break
		}
	}
	if !diff {
		t.Fatal("two different salts produced identical marker assignments")
	}
}

// TestMarkerPoolCoversPurposes guards the invariant the distinct assignment needs.
func TestMarkerPoolCoversPurposes(t *testing.T) {
	if len(markerPool) < len(markerPurposes) {
		t.Fatalf("markerPool (%d) must be >= markerPurposes (%d)", len(markerPool), len(markerPurposes))
	}
	// markerPool and sentinelPool must be disjoint (a sentinel basename must never
	// collide with a salt-derived marker in the same dir).
	m := map[string]bool{}
	for _, x := range markerPool {
		m[x] = true
	}
	for _, s := range sentinelPool {
		if m[s] {
			t.Fatalf("markerPool and sentinelPool share %q — a sentinel could overwrite a marker", s)
		}
	}
}

// TestDaemonArgv0DistinctPerSeedStable: the daemon display token is derived from
// the role seed (distinct labels → usually distinct tokens), stable per seed, and
// empty for an empty seed.
func TestDaemonArgv0DistinctPerSeedStable(t *testing.T) {
	if DaemonArgv0("") != "" {
		t.Fatal("empty seed must give empty token (legacy visible argv)")
	}
	a := DaemonArgv0("com.apple.coreservices.spotlight")
	if a == "" {
		t.Fatal("non-empty seed must give a token")
	}
	if a != DaemonArgv0("com.apple.coreservices.spotlight") {
		t.Fatal("token must be stable per seed")
	}
	// The token must be a plausible helper name from the shared pool.
	found := false
	for _, tok := range procTokens {
		if tok == a {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("token %q not drawn from procTokens", a)
	}
	// Distinct role seeds should (usually) give distinct tokens; assert at least
	// two of three sample labels differ so the three mesh procs don't cluster.
	toks := map[string]bool{
		DaemonArgv0("com.apple.coreservices.spotlight"): true,
		DaemonArgv0("MicrosoftUpdateHelper"):            true,
		DaemonArgv0("trustlocationd"):                   true,
	}
	if len(toks) < 2 {
		t.Fatalf("three role tokens clustered to one value: %v", toks)
	}
}
