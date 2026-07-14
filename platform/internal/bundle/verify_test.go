package bundle

import (
	"os"
	"path/filepath"
	"testing"
)

// firstPluginSubdir returns the name of any plugin subdir present in the
// embedded bundle, plus the path to its binary on disk after an extract.
func firstPluginSubdir(t *testing.T, root string) (subdir, binPath string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Find an extensionless file inside (the plugin binary).
		sub := filepath.Join(root, e.Name())
		files, _ := os.ReadDir(sub)
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			if !containsAny(f.Name(), ".") {
				return e.Name(), filepath.Join(sub, f.Name())
			}
		}
	}
	return "", ""
}

// TestVerifyOrRestore_SwapAndRestore is AC-1/AC-3: a plugin binary
// overwritten with a substitute is detected and atomically restored to the
// genuine version; VerifyOrRestore reports restored=true and the on-disk
// content hashes back to the genuine embedded bytes.
func TestVerifyOrRestore_SwapAndRestore(t *testing.T) {
	if !HasAny() {
		t.Skip("no bundled plugins in this build; skipping")
	}
	root := t.TempDir()
	if _, err := ExtractTo(root); err != nil {
		t.Fatalf("initial extract: %v", err)
	}
	subdir, binPath := firstPluginSubdir(t, root)
	if subdir == "" {
		t.Skip("no extensionless plugin binary in this build; skipping")
	}
	genuine, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read genuine: %v", err)
	}

	// Swap in a do-nothing stand-in.
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("swap binary: %v", err)
	}

	restored, wantPrefix, gotPrefix, err := VerifyOrRestore(root, subdir)
	if err != nil {
		t.Fatalf("VerifyOrRestore: %v", err)
	}
	if !restored {
		t.Fatal("expected restored=true after swap")
	}
	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read after restore: %v", err)
	}
	if sha(got) != sha(genuine) {
		t.Fatal("binary not restored to genuine content")
	}
	// Prefixes: want is the genuine sha prefix, got is the substitute's —
	// non-empty, distinct, and not the full digest (12 hex chars).
	if len(wantPrefix) != 12 || len(gotPrefix) != 12 {
		t.Fatalf("want/got prefixes should be 12 hex chars: want=%q got=%q", wantPrefix, gotPrefix)
	}
	if wantPrefix == gotPrefix {
		t.Errorf("a content swap should yield differing prefixes: %q == %q", wantPrefix, gotPrefix)
	}
	if wantPrefix != shaPrefix(sha(genuine)) {
		t.Errorf("wantPrefix %q != genuine prefix %q", wantPrefix, shaPrefix(sha(genuine)))
	}
}

// TestVerifyOrRestore_RestoresSwappedManifest is FEATURE 23, Fix 1: a swapped
// plugin.json (the file that drives entrypoint/run_as resolution) is detected
// and restored to the genuine embedded copy — so discovery's verify-before-
// parse reads authentic bytes, not an attacker's redirected manifest.
func TestVerifyOrRestore_RestoresSwappedManifest(t *testing.T) {
	if !HasAny() {
		t.Skip("no bundled plugins in this build; skipping")
	}
	root := t.TempDir()
	if _, err := ExtractTo(root); err != nil {
		t.Fatalf("initial extract: %v", err)
	}
	subdir, _ := firstPluginSubdir(t, root)
	if subdir == "" {
		t.Skip("no extensionless plugin binary in this build; skipping")
	}
	manifestPath := filepath.Join(root, subdir, "plugin.json")
	genuine, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read genuine manifest: %v", err)
	}
	// Tamper the manifest: a redirected entrypoint is the classic attack.
	tampered := `{"id":"x","name":"x","version":"1.0.0","type":"job",` +
		`"protocol_version":"1","entrypoint":"../evil","run_as":"system"}`
	if err := os.WriteFile(manifestPath, []byte(tampered), 0o644); err != nil {
		t.Fatalf("swap manifest: %v", err)
	}
	restored, _, _, err := VerifyOrRestore(root, subdir)
	if err != nil {
		t.Fatalf("VerifyOrRestore: %v", err)
	}
	if !restored {
		t.Fatal("a swapped plugin.json must be restored")
	}
	got, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read after restore: %v", err)
	}
	if string(got) != string(genuine) {
		t.Fatal("plugin.json not restored to genuine content")
	}
}

// TestIsBundled is FEATURE 23, Fix 3: the system-mode allowlist primitive. A
// plugin subdir present in the embedded bundle is bundled; an unknown name or
// any invalid/path-like value is not (fail closed).
func TestIsBundled(t *testing.T) {
	if !HasAny() {
		t.Skip("no bundled plugins in this build; skipping")
	}
	root := t.TempDir()
	if _, err := ExtractTo(root); err != nil {
		t.Fatalf("extract: %v", err)
	}
	subdir, _ := firstPluginSubdir(t, root)
	if subdir == "" {
		t.Skip("no bundled plugin subdir in this build; skipping")
	}
	if !IsBundled(subdir) {
		t.Errorf("bundled subdir %q reported not bundled", subdir)
	}
	for _, bad := range []string{"definitely-not-a-plugin", "", ".", "..", "a/b", "../x", "./x"} {
		if IsBundled(bad) {
			t.Errorf("non-bundled/invalid subdir %q must report false", bad)
		}
	}
}

// TestVerifyOrRestore_CleanFastPath is AC-5: on an untampered install no
// file is rewritten and restored is false.
func TestVerifyOrRestore_CleanFastPath(t *testing.T) {
	if !HasAny() {
		t.Skip("no bundled plugins in this build; skipping")
	}
	root := t.TempDir()
	if _, err := ExtractTo(root); err != nil {
		t.Fatalf("initial extract: %v", err)
	}
	subdir, _ := firstPluginSubdir(t, root)
	if subdir == "" {
		t.Skip("no extensionless plugin binary in this build; skipping")
	}
	restored, wantPrefix, gotPrefix, err := VerifyOrRestore(root, subdir)
	if err != nil {
		t.Fatalf("VerifyOrRestore: %v", err)
	}
	if restored {
		t.Fatal("clean install must not report restored")
	}
	if wantPrefix != "" || gotPrefix != "" {
		t.Errorf("clean install must not surface prefixes: want=%q got=%q", wantPrefix, gotPrefix)
	}
}

// TestVerifyOrRestore_RepairsMode: a binary whose +x was stripped (content
// intact) gets its mode repaired on the fast path without a content rewrite.
func TestVerifyOrRestore_RepairsMode(t *testing.T) {
	if !HasAny() {
		t.Skip("no bundled plugins in this build; skipping")
	}
	root := t.TempDir()
	if _, err := ExtractTo(root); err != nil {
		t.Fatalf("initial extract: %v", err)
	}
	subdir, binPath := firstPluginSubdir(t, root)
	if subdir == "" {
		t.Skip("no extensionless plugin binary in this build; skipping")
	}
	if err := os.Chmod(binPath, 0o644); err != nil {
		t.Fatalf("strip +x: %v", err)
	}
	restored, _, _, err := VerifyOrRestore(root, subdir)
	if err != nil {
		t.Fatalf("VerifyOrRestore: %v", err)
	}
	if restored {
		t.Error("mode-only repair must not count as a content restore")
	}
	info, err := os.Stat(binPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode not repaired: got %o, want 0755", info.Mode().Perm())
	}
}

// TestVerifyOrRestore_UnknownSubdir: a subdir not in the bundle is a
// no-op, never an error (non-bundled plugins are simply not covered).
func TestVerifyOrRestore_UnknownSubdir(t *testing.T) {
	root := t.TempDir()
	restored, _, _, err := VerifyOrRestore(root, "does-not-exist")
	if err != nil {
		t.Fatalf("unknown subdir should not error: %v", err)
	}
	if restored {
		t.Fatal("unknown subdir should not restore anything")
	}
}

// TestVerifyOrRestore_GuardsEmptyOrDotSubdir is Fix 4: a subdir that would
// resolve to the whole bundle ("" or "." or ".." or one that is not a single
// path element) must be rejected with an error, never silently walked — a
// point-of-use check must scope to exactly one plugin. The guard validates
// the CLEANED value (so "./x" → "x" is handled, see below) and checks both
// '/' and the host os.PathSeparator. A backslash is a path separator only on
// Windows, so "a\\b" is asserted host-conditionally further down.
func TestVerifyOrRestore_GuardsEmptyOrDotSubdir(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []string{"", ".", "..", "a/b", "../x"} {
		restored, _, _, err := VerifyOrRestore(root, bad)
		if err == nil {
			t.Errorf("subdir %q must be rejected, got no error", bad)
		}
		if restored {
			t.Errorf("subdir %q must not restore anything", bad)
		}
	}
	// A literal backslash is a path separator (nested path) ONLY on Windows;
	// on Unix it is a legal single-element filename. Assert the platform's
	// actual behaviour via filepath: if Base() differs from the input, the
	// guard must reject it.
	const backslash = `a\b`
	wantReject := backslash != filepath.Base(backslash)
	_, _, _, berr := VerifyOrRestore(root, backslash)
	if wantReject && berr == nil {
		t.Errorf("subdir %q must be rejected on this OS, got no error", backslash)
	}
	if !wantReject && berr != nil {
		t.Errorf("subdir %q is a valid single element on this OS, got error: %v", backslash, berr)
	}
	// "./x" cleans to a single element "x" but, being a non-existent plugin,
	// must be treated as an unknown (no-op) subdir — i.e. it passes the guard
	// (no error) yet restores nothing. This proves the guard uses the cleaned
	// value rather than rejecting on the raw "./" prefix.
	restored, _, _, err := VerifyOrRestore(root, "./x")
	if err != nil {
		t.Errorf(`"./x" should clean to a valid single element, got error: %v`, err)
	}
	if restored {
		t.Error(`"./x" is not a real plugin; nothing should be restored`)
	}
	// A valid single-element name passes the guard (unknown bundle ⇒ no-op).
	if _, _, _, err := VerifyOrRestore(root, "kill-steam"); err != nil {
		t.Errorf("a valid single-element subdir must be accepted, got: %v", err)
	}
}
