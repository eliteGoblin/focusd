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

	restored, err := VerifyOrRestore(root, subdir)
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
	restored, err := VerifyOrRestore(root, subdir)
	if err != nil {
		t.Fatalf("VerifyOrRestore: %v", err)
	}
	if restored {
		t.Fatal("clean install must not report restored")
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
	restored, err := VerifyOrRestore(root, subdir)
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
	restored, err := VerifyOrRestore(root, "does-not-exist")
	if err != nil {
		t.Fatalf("unknown subdir should not error: %v", err)
	}
	if restored {
		t.Fatal("unknown subdir should not restore anything")
	}
}
