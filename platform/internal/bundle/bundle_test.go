package bundle

import (
	"os"
	"path/filepath"
	"testing"
)

// On the same-content fast path, ExtractTo must still repair the
// expected file mode — otherwise a plugin binary that lost +x (manual
// chmod, restore-from-backup, weird umask) would never get fixed and
// the platform would silently fail to exec it. (Copilot review.)
func TestExtractTo_FastPath_RepairsLostExecBit(t *testing.T) {
	if !HasAny() {
		t.Skip("no bundled plugins in this build; skipping")
	}
	root := t.TempDir()

	// First extraction lays everything down with the right modes.
	if _, err := ExtractTo(root); err != nil {
		t.Fatalf("initial extract: %v", err)
	}

	// Find any extracted plugin binary (extensionless basename, 0o755 in
	// the heuristic) and forcibly strip its +x.
	var binPath string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || binPath != "" {
			return err
		}
		base := filepath.Base(p)
		if !containsAny(base, ".") {
			binPath = p
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if binPath == "" {
		t.Skip("no extensionless plugin binary in this build; skipping")
	}
	if err := os.Chmod(binPath, 0o644); err != nil {
		t.Fatalf("chmod down: %v", err)
	}

	// Second extraction takes the fast path (content matches embed) and
	// must restore 0o755 on the binary.
	if _, err := ExtractTo(root); err != nil {
		t.Fatalf("repeat extract: %v", err)
	}
	info, err := os.Stat(binPath)
	if err != nil {
		t.Fatalf("stat after repair: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("mode not repaired on fast path: got %o, want %o", got, 0o755)
	}
}

func containsAny(s, chars string) bool {
	for _, c := range chars {
		for _, r := range s {
			if c == r {
				return true
			}
		}
	}
	return false
}
