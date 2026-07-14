package platformsvc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestStartCapturesEngineLogToFile is the observability guard: the engine's
// stdout AND stderr must be captured to <workdir>/platform.log. Previously the
// child's stdio was left nil → /dev/null, silently discarding every engine and
// plugin log line (and hiding real failures). A fake engine writes to both
// streams; both must show up in the log file.
func TestStartCapturesEngineLogToFile(t *testing.T) {
	wd := t.TempDir()
	script := filepath.Join(wd, "fake-engine")
	body := "#!/bin/sh\necho ENGINE_STDOUT_LINE\necho ENGINE_STDERR_LINE >&2\nsleep 0.3\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	p := New(wd)
	if err := p.Start(script, "v1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case <-p.exitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("engine did not exit in time")
	}

	b, err := os.ReadFile(filepath.Join(wd, PlatformLogName))
	if err != nil {
		t.Fatalf("read %s: %v", PlatformLogName, err)
	}
	got := string(b)
	if !strings.Contains(got, "ENGINE_STDOUT_LINE") {
		t.Errorf("engine stdout not captured to %s; got: %q", PlatformLogName, got)
	}
	if !strings.Contains(got, "ENGINE_STDERR_LINE") {
		t.Errorf("engine stderr not captured to %s; got: %q", PlatformLogName, got)
	}
}

// TestStartAppendsAcrossRestarts confirms a restart appends (doesn't truncate)
// — log history across engine restarts must be preserved.
func TestStartAppendsAcrossRestarts(t *testing.T) {
	wd := t.TempDir()
	script := filepath.Join(wd, "fake-engine")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho RUN_MARKER\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := New(wd)
	for i := 0; i < 2; i++ {
		if err := p.Start(script, "v1"); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		select {
		case <-p.exitCh:
		case <-time.After(3 * time.Second):
			t.Fatalf("engine restart %d did not exit in time", i)
		}
	}
	b, err := os.ReadFile(filepath.Join(wd, PlatformLogName))
	if err != nil {
		t.Fatalf("read %s: %v", PlatformLogName, err)
	}
	if n := strings.Count(string(b), "RUN_MARKER"); n != 2 {
		t.Errorf("expected 2 appended markers across restarts, got %d", n)
	}
}

// --- P3 (HF4) salt-independent liveness pidfile mechanics -------------------

// TestWritePidFile pins the atomic temp+rename write: the pid lands in the file
// and the PID-unique temp is renamed away (never left behind for a concurrent
// reader to trip over), and a second write atomically replaces the value.
func TestWritePidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pid")
	if err := writePidFile(path, 4242); err != nil {
		t.Fatalf("writePidFile: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pidfile: %v", err)
	}
	if got := strings.TrimSpace(string(b)); got != "4242" {
		t.Fatalf("pidfile holds %q, want 4242", got)
	}
	// The atomic write must leave no `.tmp.<pid>` sibling behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Fatalf("stray temp file survived the rename: %s", e.Name())
		}
	}
	// A rewrite atomically replaces the value in place.
	if err := writePidFile(path, 777); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	b, _ = os.ReadFile(path)
	if got := strings.TrimSpace(string(b)); got != "777" {
		t.Fatalf("after rewrite pidfile holds %q, want 777", got)
	}
}

// TestRemovePidIfMatches: the guarded removal deletes the pidfile only while it
// still names the current pid, and is a no-op (leaves the file untouched, no
// error) when it holds a DIFFERENT pid — the guard a stale exit waiter relies on
// to never delete a newer child's entry.
func TestRemovePidIfMatches(t *testing.T) {
	t.Run("removes when file still holds the pid", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "pid")
		if err := writePidFile(path, 555); err != nil {
			t.Fatal(err)
		}
		if err := removePidIfMatches(path, 555); err != nil {
			t.Fatalf("removePidIfMatches: %v", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("pidfile holding the current pid must be removed")
		}
	})
	t.Run("no-op when file holds a different pid", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "pid")
		if err := writePidFile(path, 555); err != nil {
			t.Fatal(err)
		}
		if err := removePidIfMatches(path, 999); err != nil {
			t.Fatalf("mismatch must not error: %v", err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("pidfile with a different pid must be left in place")
		}
		if got := strings.TrimSpace(string(b)); got != "555" {
			t.Fatalf("pidfile clobbered: holds %q, want 555", got)
		}
	})
}

// TestStartRewriteThenStaleRemoveNoClobber models the interleaving the pidfile
// guard defends against: a new Start (child B) rewrites the pidfile with its own
// pid before a PRIOR child A's exit waiter fires removePidIfMatches(A). Because
// the file now names B, the stale removal is a no-op and B's live entry survives.
func TestStartRewriteThenStaleRemoveNoClobber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pid")
	const oldPID, newPID = 111, 222
	// Old child A published its pid.
	if err := writePidFile(path, oldPID); err != nil {
		t.Fatal(err)
	}
	// New child B's Start rewrites the pidfile with its own pid (both writes are
	// serialized under p.mu in production).
	if err := writePidFile(path, newPID); err != nil {
		t.Fatal(err)
	}
	// A's stale exit waiter now fires — the file holds B, so it must NOT clobber.
	if err := removePidIfMatches(path, oldPID); err != nil {
		t.Fatalf("stale remove must not error: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("the new child's pidfile must survive the stale waiter")
	}
	if got := strings.TrimSpace(string(b)); got != strconv.Itoa(newPID) {
		t.Fatalf("pidfile clobbered by stale waiter: holds %q, want %d", got, newPID)
	}
}

// TestStartWritesAndClearsPidFile exercises the real ProcSvc.PidFile lifecycle:
// Start publishes the child's pid (synchronously, under p.mu) and the single exit
// waiter removes the pidfile before closing exitCh — so a `status` reader sees an
// accurate liveness signal while the child runs and no stale file after it exits.
func TestStartWritesAndClearsPidFile(t *testing.T) {
	wd := t.TempDir()
	pidPath := filepath.Join(wd, "child.pid")
	script := filepath.Join(wd, "fake-engine")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 0.3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := New(wd)
	p.PidFile = pidPath
	if err := p.Start(script, "v1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Start writes the pidfile synchronously before returning.
	b, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("Start must publish the pidfile: %v", err)
	}
	got, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("pidfile holds non-int %q", b)
	}
	if pid := p.RunningPID(); got != pid {
		t.Fatalf("pidfile holds %d, want the child pid %d", got, pid)
	}
	select {
	case <-p.exitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("engine did not exit in time")
	}
	// The waiter removes the pidfile BEFORE closing exitCh, so it is gone now.
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatal("exit waiter must remove the pidfile")
	}
}

// TestClearExitForgetsDeadChildCrash proves ClearExit resets the crash-loop
// latch: a fast-exiting child leaves CrashedQuickly(v)==true, and after
// ClearExit the daemon no longer sees a crash to re-count (nor a live child).
// This is the FIX 1 defense-in-depth seam — reverting an in-place tamper must
// not leave the just-replaced version wrongly suspected of crash-looping.
func TestClearExitForgetsDeadChildCrash(t *testing.T) {
	wd := t.TempDir()
	script := filepath.Join(wd, "fast-exit")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := New(wd)
	p.Unhealthy = 3 * time.Second // exit sooner than this ⇒ "crashed quickly"
	if err := p.Start(script, "v1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case <-p.exitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("fast-exit child did not exit in time")
	}
	if !p.CrashedQuickly("v1") {
		t.Fatal("pre-condition: a fast-exiting child must read as CrashedQuickly")
	}

	p.ClearExit()

	if p.CrashedQuickly("v1") {
		t.Fatal("ClearExit must clear the crash latch: CrashedQuickly still true")
	}
	if v, _ := p.RunningVersion(); v != "" {
		t.Fatalf("ClearExit must not claim a live child, RunningVersion=%q", v)
	}
}

// TestClearExitNoopWhileLive proves ClearExit never disturbs a RUNNING child:
// while a child is alive it is a no-op, so it can never be used to blind the
// daemon to a live platform.
func TestClearExitNoopWhileLive(t *testing.T) {
	wd := t.TempDir()
	script := filepath.Join(wd, "long-lived")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := New(wd)
	if err := p.Start(script, "v1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer p.Stop()

	p.ClearExit() // no-op: child is live

	if v, _ := p.RunningVersion(); v != "v1" {
		t.Fatalf("ClearExit must not disturb a live child, RunningVersion=%q", v)
	}
}
