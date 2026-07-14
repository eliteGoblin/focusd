//go:build darwin

package osadapter

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
)

// TestReapHelperProcess is NOT a real test. When FOCUSD_REAP_HELPER=1 it blocks
// forever so the parent can treat this process as a stand-in platform (the
// standard os/exec helper-process pattern). Under a normal `go test` run (env
// unset) it returns immediately and does nothing.
func TestReapHelperProcess(t *testing.T) {
	if os.Getenv("FOCUSD_REAP_HELPER") != "1" {
		return
	}
	select {} // block until the parent signals us to die
}

// spawnFakePlatform copies the running test binary to
// <supportRoot>/<disguise>/bin/<ver>/platform and execs it as a blocking
// process, so `ps` reports argv[0] = that path — the exact platform signature
// the reaper matches. Returns the child pid.
func spawnFakePlatform(t *testing.T, supportRoot, disguise, ver string) int {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	pw := filepath.Join(supportRoot, disguise)
	binDir := filepath.Join(pw, "bin", ver)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	binPath := filepath.Join(binDir, "platform")
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read self: %v", err)
	}
	if err := os.WriteFile(binPath, data, 0o755); err != nil {
		t.Fatalf("write fake platform: %v", err)
	}
	// -test.run pins the child to ONLY the blocking helper test; -- separates the
	// platform's own args so argv still begins with binPath (the signature).
	cmd := exec.Command(binPath, "-test.run=^TestReapHelperProcess$", "--", "--workdir", pw)
	cmd.Env = append(os.Environ(), "FOCUSD_REAP_HELPER=1")
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake platform: %v", err)
	}
	pid := cmd.Process.Pid
	// Single reaper goroutine: as soon as the child exits (naturally or from a
	// reap SIGKILL) we Wait() it so it does NOT linger as a zombie — otherwise
	// syscall.Kill(pid,0) would keep reporting a dead-but-unreaped child as
	// "alive" and mask the kill.
	go func() { _, _ = cmd.Process.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() }) // goroutine reaps; Kill is best-effort
	waitPlatformVisible(t, supportRoot, pid)
	return pid
}

// waitPlatformVisible blocks until pid is enumerable AND classified as a
// platform under supportRoot (so a test never races the child's ps visibility).
func waitPlatformVisible(t *testing.T, supportRoot string, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		procs, err := listPlatformProcs()
		if err == nil {
			for _, p := range procs {
				if p.pid != pid {
					continue
				}
				if _, ok := classifyPlatformArgv(p.cmd, supportRoot); ok {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fake platform pid %d never became visible under %s", pid, supportRoot)
}

// isAlive reports whether pid exists (signal 0 probes without delivering).
func isAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// waitDead blocks up to 3s for pid to exit (SIGTERM→SIGKILL takes a moment to
// reap). Fails the test if it is still alive after the window.
func waitDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !isAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d still alive after reap", pid)
}

// --- classify: pure anchoring + signature ---------------------------------

// TestClassifyPlatformArgv feeds EXECUTABLE PATHS (the `ps comm=` column — argv0
// only, no arguments) exactly as the reaper sees them.
func TestClassifyPlatformArgv(t *testing.T) {
	const root = "/Users/x/Library/Application Support"
	cases := []struct {
		name     string
		execPath string
		wantOK   bool
	}{
		{
			// real-world: disguised platform under a root WITH a space.
			name:     "disguised under root with space",
			execPath: "/Users/x/Library/Application Support/.io.tailscale.d/bin/v0.16.7/platform",
			wantOK:   true,
		},
		{
			name:     "prerelease semver",
			execPath: "/Users/x/Library/Application Support/.d/bin/v1.2.3-rc.1/platform",
			wantOK:   true,
		},
		{
			name:     "signature present but OUTSIDE root — not ours",
			execPath: "/opt/vendor/bin/v1.0.0/platform",
			wantOK:   false,
		},
		{
			name:     "under root but NOT the platform signature",
			execPath: "/Users/x/Library/Application Support/.d/bin/v1.0.0/daemon",
			wantOK:   false,
		},
		{
			// Finding #2 regression: a sibling binary sharing the versioned bin
			// dir must NOT match — the `$`-anchored signature is the whole
			// basename `platform`, never a prefix of `platform-debug`.
			name:     "sibling platform-debug must NOT match",
			execPath: "/Users/x/Library/Application Support/.d/bin/v1.0.0/platform-debug",
			wantOK:   false,
		},
		{
			name:     "basename-only comm (relative) — not ours",
			execPath: "cfprefsd",
			wantOK:   false,
		},
		{
			name:     "no version segment",
			execPath: "/Users/x/Library/Application Support/.d/bin/platform",
			wantOK:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := classifyPlatformArgv(c.execPath, root)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (execPath=%q)", ok, c.wantOK, c.execPath)
			}
			if ok && got != c.execPath {
				t.Fatalf("path = %q, want %q", got, c.execPath)
			}
		})
	}
}

// TestReapRefusesUnanchoredRoot: an empty/relative supportRoot reaps NOTHING
// (the belt that stops an unanchored match from ever killing a process).
func TestReapRefusesUnanchoredRoot(t *testing.T) {
	killed := 0
	list := func() ([]rawProc, error) {
		return []rawProc{{pid: 999, cmd: "/a/bin/v1.0.0/platform"}}, nil
	}
	kill := func(int) { killed++ }
	for _, root := range []string{"", "relative/root"} {
		n, err := reapForeignPlatforms(root, 0, "", list, kill)
		if err != nil || n != 0 || killed != 0 {
			t.Fatalf("root=%q must reap nothing, got n=%d killed=%d err=%v", root, n, killed, err)
		}
	}
}

// TestReapExemptsByPath: the survivor is exempt by PATH even when its PID is
// unknown (keepPID=0) — the install-time convergence case.
func TestReapExemptsByPath(t *testing.T) {
	root := "/r"
	survivor := "/r/.keep/bin/v1.0.0/platform"
	orphan := "/r/.old/bin/v0.9.0/platform"
	var killed []int
	list := func() ([]rawProc, error) {
		return []rawProc{
			{pid: 10, cmd: survivor},
			{pid: 20, cmd: orphan},
		}, nil
	}
	kill := func(pid int) { killed = append(killed, pid) }
	n, err := reapForeignPlatforms(root, 0, survivor, list, kill)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(killed) != 1 || killed[0] != 20 {
		t.Fatalf("want only orphan pid 20 reaped, got n=%d killed=%v", n, killed)
	}
}

// --- Test #3: steady-state extra platform reaped (REAL processes) ----------

// TestReapForeignPlatforms_ReapsExtraExemptsSurvivor spawns two REAL platform
// processes under a sandbox support root, then reaps exempting the survivor's
// PID: the extra is SIGTERM→SIGKILLed and the survivor is untouched. Every kill
// is gated on a positive existence check of the exact target first.
func TestReapForeignPlatforms_ReapsExtraExemptsSurvivor(t *testing.T) {
	root := t.TempDir()
	survivorPID := spawnFakePlatform(t, root, ".keep", "v0.16.3")
	orphanPID := spawnFakePlatform(t, root, ".orphan", "v0.16.3")

	// GATE: both targets must genuinely exist before we act.
	if !isAlive(survivorPID) || !isAlive(orphanPID) {
		t.Fatalf("pre-condition: both platforms must be alive (survivor=%v orphan=%v)",
			isAlive(survivorPID), isAlive(orphanPID))
	}

	n, err := reapForeignPlatforms(root, survivorPID, "", listPlatformProcs, killProc)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want exactly 1 (the orphan)", n)
	}
	waitDead(t, orphanPID)
	if !isAlive(survivorPID) {
		t.Fatal("survivor must NOT be reaped (exempt by PID)")
	}
}

// --- Test #4: the 16-platform repro (REAL processes + flock election) ------

// TestReapForeignPlatforms_SixteenPlatformRepro reproduces the accretion bug in
// miniature and proves the fix converges to ONE platform:
//
//  1. Daemon D1 holds the singleton flock and runs platform #1.
//  2. D1 is SIGKILLed (crash/self-update). Its platform child reparents to
//     launchd and SURVIVES — now an orphan. The kernel frees the flock.
//  3. Standby daemon D2 acquires the freed flock and starts platform #2.
//  4. D2's reap tick reaps foreign platforms exempting ITS survivor (#2): the
//     orphan #1 dies and EXACTLY ONE platform remains.
//
// This is the whole "every crash adds one → ~16" chain in a single cycle; the
// reaper is what the flock election always lacked.
func TestReapForeignPlatforms_SixteenPlatformRepro(t *testing.T) {
	root := t.TempDir()
	lockPath := filepath.Join(root, "singleton.lock")

	// (1) D1 wins the flock and runs platform #1.
	d1 := core.NewFileLock()
	if ok, err := d1.TryAcquire(lockPath); err != nil || !ok {
		t.Fatalf("D1 must win the flock: ok=%v err=%v", ok, err)
	}
	orphanPID := spawnFakePlatform(t, root, ".gen1", "v0.16.3") // platform #1

	// (2) D1 "dies": release the flock (kernel does this on process death). Its
	// platform child does NOT die with it — it is already an independent process
	// (the reparent-to-launchd survival this feature fixes), modeled here by the
	// spawned process outliving the lock release.
	if err := d1.Release(); err != nil {
		t.Fatalf("release D1 flock: %v", err)
	}
	if !isAlive(orphanPID) {
		t.Fatal("orphaned platform #1 must survive D1's death")
	}

	// (3) Standby D2 acquires the freed flock and starts platform #2.
	d2 := core.NewFileLock()
	if ok, err := d2.TryAcquire(lockPath); err != nil || !ok {
		t.Fatalf("D2 must acquire the freed flock: ok=%v err=%v", ok, err)
	}
	defer d2.Release()
	survivorPID := spawnFakePlatform(t, root, ".gen2", "v0.16.3") // platform #2

	// Pre-condition: BOTH platforms are live (the accretion the bug produced).
	if !isAlive(orphanPID) || !isAlive(survivorPID) {
		t.Fatal("both platforms must be alive before the reap tick")
	}

	// (4) D2's reap tick exempts its own survivor and reaps the orphan.
	n, err := reapForeignPlatforms(root, survivorPID, "", listPlatformProcs, killProc)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want exactly 1 (orphan #1)", n)
	}
	waitDead(t, orphanPID)
	if !isAlive(survivorPID) {
		t.Fatal("the surviving platform #2 must remain — reap must never reach zero")
	}

	// EXACTLY ONE platform under this root remains.
	remaining := 0
	procs, _ := listPlatformProcs()
	for _, p := range procs {
		if _, ok := classifyPlatformArgv(p.cmd, root); ok && isAlive(p.pid) {
			remaining++
		}
	}
	if remaining != 1 {
		t.Fatalf("want exactly ONE platform after convergence, got %d", remaining)
	}
}
