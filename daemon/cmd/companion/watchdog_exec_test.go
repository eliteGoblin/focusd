package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestExecWatchdogCtxTimesOut (#106-b3): a genuinely hung watchdog must be KILLED
// once the timeout elapses so the companion one-shot exits (freeing launchd's
// cadence) instead of wedging forever. We point the "daemon" at a script that
// ignores its args and sleeps far longer than the timeout; execWatchdogCtx must
// return a non-nil error PROMPTLY (well under the sleep), proving the context
// deadline killed the child.
func TestExecWatchdogCtxTimesOut(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hang")
	// `exec sleep 30` so the script process BECOMES sleep (same PID) and
	// exec.CommandContext's SIGKILL reaps it directly (no orphaned child).
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	err := execWatchdogCtx(context.Background(), script, "v0.1.0", 150*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("a hung watchdog must be killed and surface an error, got nil")
	}
	// Must return FAR sooner than the 30s sleep (proves the deadline fired). Generous
	// upper bound tolerates the WaitDelay safety cap + loaded CI, still << 30s.
	if elapsed > 15*time.Second {
		t.Fatalf("execWatchdogCtx did not honor the timeout (elapsed %v, sleep was 30s)", elapsed)
	}
}

// TestExecWatchdogCtxSucceedsWithinTimeout: a fast, well-behaved watchdog run
// returns nil without being cut off by the timeout.
func TestExecWatchdogCtxSucceedsWithinTimeout(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "ok")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A generous timeout: this asserts a fast run is NOT cut off — the deadline must
	// never be the bottleneck, even under heavy parallel -race process-spawn latency.
	if err := execWatchdogCtx(context.Background(), script, "v0.1.0", 60*time.Second); err != nil {
		t.Fatalf("a fast watchdog run within the timeout must succeed, got %v", err)
	}
}
