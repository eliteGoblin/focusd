//go:build darwin

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
	// Every returned proc must carry a usable identity (name, since the
	// sysctl path leaves Path empty) and a real pid.
	for _, p := range procs {
		if p.PID <= 0 || p.Name == "" {
			t.Errorf("malformed procView %+v (pid>0 and Name required)", p)
			break
		}
	}
}

// Regression guard for the CGO_ENABLED=0 timeout bug: the gopsutil-based
// scan fell back to spawning `lsof -p` per process (~26s for ~850 procs),
// blowing past the 20s platform job timeout so the plugin was SIGKILLed
// every reconcile. The sysctl path is a single syscall and must finish in
// well under a second even on a busy machine. We assert a generous 2s
// ceiling (the real measurement is orders of magnitude faster) so the test
// is not flaky but still fails hard if anyone reintroduces a per-process
// fork. This test is meaningful precisely under CGO_ENABLED=0.
func TestListProcessesIsFast(t *testing.T) {
	const ceiling = 2 * time.Second
	start := time.Now()
	procs, err := listProcesses()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("listProcesses: %v", err)
	}
	t.Logf("listProcesses scanned %d procs in %s", len(procs), elapsed)
	if elapsed > ceiling {
		t.Fatalf("listProcesses took %s (> %s): per-process fork regression?",
			elapsed, ceiling)
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
