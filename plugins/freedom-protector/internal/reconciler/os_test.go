package reconciler

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// listProcesses must enumerate the live table without launching/killing
// anything; this test process itself must appear.
func TestListProcessesRealIsSafe(t *testing.T) {
	procs, err := listProcesses()
	if err != nil {
		t.Fatalf("listProcesses: %v", err)
	}
	if len(procs) == 0 {
		t.Error("expected at least this test process")
	}
}

// pathExists reports real on-disk presence. A temp file exists; a bogus
// sibling does not.
func TestPathExistsReal(t *testing.T) {
	dir := t.TempDir()
	if !pathExists(dir) {
		t.Errorf("expected temp dir %q to exist", dir)
	}
	if pathExists(filepath.Join(dir, "definitely-not-here")) {
		t.Error("expected missing path to report absent")
	}
}

// launchDetached against a real, fast, side-effect-free binary (`true`)
// must start and release promptly without hanging. We never launch
// Freedom here — `true` exits 0 immediately.
func TestLaunchDetachedRealHarmlessBinary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := launchDetached(ctx, "true"); err != nil {
		t.Fatalf("launchDetached(true): %v", err)
	}
}

// launchDetached must surface a start error for a non-existent binary
// rather than hang.
func TestLaunchDetachedMissingBinary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := launchDetached(ctx, "/nonexistent/freedom-protector-test-binary"); err == nil {
		t.Error("expected start error for missing binary")
	}
}
