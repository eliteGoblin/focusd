//go:build darwin

package osadapter

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

// --- fake-platform spawning + test seams -----------------------------------

// spawnFakePlatformAt copies the running test binary to binPath and execs it as
// a blocking process. Because we do not override argv, the child's kernel argv0
// is binPath, and its `txt` executable path (what resolvePlatformExecs reads via
// lsof) is also binPath — so the reaper's signature tier can VerifyFile it and
// the deleted-binary fallback can match its argv0. Returns the child pid, waiting
// until lsof reports it (no ps/lsof visibility race).
func spawnFakePlatformAt(t *testing.T, binPath string) int {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read self: %v", err)
	}
	if err := os.WriteFile(binPath, data, 0o755); err != nil {
		t.Fatalf("write fake platform: %v", err)
	}
	// -test.run pins the child to ONLY the blocking helper; FOCUSD_REAP_HELPER=1
	// makes that helper select{} forever. -test.timeout=10m is REQUIRED: a bare
	// select{} with no pending timer trips Go's deadlock detector ("all goroutines
	// are asleep") and the child panics/exits immediately; a positive test timeout
	// registers a watchdog timer that keeps the scheduler live so the child blocks
	// until the parent kills it (well inside 10m). argv0 is still binPath, so `ps
	// comm=` and the lsof exec path are unaffected.
	cmd := exec.Command(binPath, "-test.run=^TestReapHelperProcess$", "-test.timeout=10m")
	cmd.Env = append(os.Environ(), "FOCUSD_REAP_HELPER=1")
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake platform: %v", err)
	}
	pid := cmd.Process.Pid
	// Single reaper goroutine: Wait() the child as soon as it exits so it does NOT
	// linger as a zombie (a dead-but-unreaped child keeps answering kill(pid,0)).
	go func() { _, _ = cmd.Process.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitExecVisible(t, pid)
	return pid
}

// spawnFakePlatform plants a fake platform at the CANONICAL, pre-HF4 layout
// <supportRoot>/<disguise>/bin/<ver>/platform. Kept for the tests that exercise
// the signature tier over a genuine-looking platform path AND the deleted-binary
// fallback (whose argv0 suffix match needs exactly this `/bin/<semver>/platform`
// shape).
func spawnFakePlatform(t *testing.T, supportRoot, disguise, ver string) int {
	t.Helper()
	binPath := filepath.Join(supportRoot, disguise, "bin", ver, "platform")
	return spawnFakePlatformAt(t, binPath)
}

// waitExecVisible blocks until lsof reports pid's executable path (so a test
// never races the child's lsof visibility before reaping). It lsof's ONLY the
// target pid (not the whole system) so the many spawn-time waits stay cheap and
// don't starve the timeout-sensitive sibling packages under `go test ./...`.
func waitExecVisible(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("lsof", "-p", strconv.Itoa(pid), "-d", "txt", "-Fn").Output()
		if strings.Contains(string(out), "\nn/") {
			return // at least one absolute txt path → executable is mapped
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fake platform pid %d never became lsof-visible", pid)
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

// testVerify mirrors sig.VerifyFile's (signed | unsigned | ENOENT) trichotomy for
// tests WITHOUT real Ed25519 signing: a present file is treated as validly signed
// unless its cleaned path is in `unsigned` (a present-but-unsigned decoy), and a
// missing file yields a wrapped fs.ErrNotExist (the deleted-binary path). A
// spawned fake platform is a copy of the unsigned test binary, so production
// sig.VerifyFile would reject it — tests inject this instead.
func testVerify(unsigned map[string]bool) Verifier {
	return func(p string) (bool, error) {
		if _, err := os.Stat(p); err != nil {
			return false, err // deleted → errors.Is(err, fs.ErrNotExist)
		}
		if unsigned[filepath.Clean(p)] {
			return false, nil // present but unsigned → mismatch
		}
		return true, nil // present → treat as genuine, signed platform
	}
}

// signedVerify accepts every present binary (no unsigned decoys).
func signedVerify() Verifier { return testVerify(nil) }

// reapRoot returns a symlink-RESOLVED temp dir. macOS symlinks /var→/private/var,
// so lsof reports a process's executable under /private/var while t.TempDir()
// hands back /var/folders/… — the reaper's SupportRoot anchor (a lexical prefix
// check, correct for production ~/Library which is not symlinked) would then
// reject every candidate. Resolving the root once makes the test root match the
// paths lsof and argv0 actually report.
func reapRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// countLivePlatformsUnder counts live processes classified as a focusd platform
// under root (survivor NOT exempted: keepClean=""), using the real ps+lsof seams
// and the given verifier — the "exactly one remains" assertion helper.
func countLivePlatformsUnder(t *testing.T, root string, verify Verifier) int {
	t.Helper()
	procs, _ := listPlatformProcs()
	execs, _ := resolvePlatformExecs()
	n := 0
	for _, p := range procs {
		if isAlive(p.pid) && classifyReapCandidate(execs[p.pid], p.cmd, root, "", verify) {
			n++
		}
	}
	return n
}

// enoent returns a wrapped fs.ErrNotExist, exactly how os.ReadFile surfaces a
// missing file (so errors.Is(err, fs.ErrNotExist) holds).
func enoent(path string) error {
	return &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
}

// --- classify: the two-tier signature + ENOENT-fallback logic ---------------

// TestClassifyReapCandidate drives the pure classifier over the full matrix that
// the signature-first / ENOENT-fallback design must honour.
func TestClassifyReapCandidate(t *testing.T) {
	const root = "/Users/x/Library/Application Support"
	// A signed, present platform under the root with a DISGUISED basename (no
	// /platform, no version) — the HF4 case the old regex could never match.
	disguised := root + "/.io.tailscale.d/bin/catalog.ab12cd34ef"
	canonical := root + "/.d/bin/v0.16.7/platform"
	// Synthetic verifiers (no disk): the classifier is pure, so drive its verify
	// seam directly rather than through sig.VerifyFile / testVerify (which stat
	// real files that these synthetic paths do not have).
	okVerify := Verifier(func(string) (bool, error) { return true, nil })   // genuine signed
	badVerify := Verifier(func(string) (bool, error) { return false, nil }) // present but unsigned

	cases := []struct {
		name     string
		execPath string
		comm     string
		keepPath string
		verify   Verifier
		want     bool
	}{
		{
			name:     "signed disguised basename under root → reap (signature tier, naming-agnostic)",
			execPath: disguised, comm: "syncagent",
			verify: okVerify, want: true,
		},
		{
			name:     "signed canonical path under root → reap",
			execPath: canonical, comm: canonical,
			verify: okVerify, want: true,
		},
		{
			name:     "present but UNSIGNED under root → NOT reaped (mismatch ≠ ENOENT)",
			execPath: canonical, comm: canonical,
			verify: badVerify, want: false,
		},
		{
			name:     "signed but OUTSIDE root → NOT reaped (anchor)",
			execPath: "/opt/vendor/bin/x", comm: "/opt/vendor/bin/x",
			verify: okVerify, want: false,
		},
		{
			name:     "deleted binary + canonical argv0 under root → reap (ENOENT fallback)",
			execPath: canonical, comm: canonical,
			verify: func(p string) (bool, error) { return false, enoent(p) }, want: true,
		},
		{
			name:     "deleted binary + DISGUISED bare-token argv0 → NOT reaped (no path to anchor)",
			execPath: disguised, comm: "syncagent",
			verify: func(p string) (bool, error) { return false, enoent(p) }, want: false,
		},
		{
			name:     "deleted binary + canonical argv0 OUTSIDE root → NOT reaped",
			execPath: "/opt/v/bin/v1.0.0/platform", comm: "/opt/v/bin/v1.0.0/platform",
			verify: func(p string) (bool, error) { return false, enoent(p) }, want: false,
		},
		{
			name:     "no exec path + canonical argv0 under root → reap (fallback works without lsof)",
			execPath: "", comm: canonical,
			verify: okVerify, want: true,
		},
		{
			name:     "read error (not ENOENT) → NOT reaped (be safe)",
			execPath: canonical, comm: "syncagent",
			verify: func(string) (bool, error) { return false, errors.New("permission denied") }, want: false,
		},
		{
			name:     "signed under root but IS the survivor path → NOT reaped (exempt)",
			execPath: canonical, comm: canonical, keepPath: canonical,
			verify: okVerify, want: false,
		},
		{
			name:     "sibling platform-debug (deleted) must NOT match the $-anchored suffix",
			execPath: root + "/.d/bin/v1.0.0/platform-debug", comm: root + "/.d/bin/v1.0.0/platform-debug",
			verify: func(p string) (bool, error) { return false, enoent(p) }, want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			keepClean := ""
			if c.keepPath != "" {
				keepClean = filepath.Clean(c.keepPath)
			}
			got := classifyReapCandidate(c.execPath, c.comm, root, keepClean, c.verify)
			if got != c.want {
				t.Fatalf("classify = %v, want %v", got, c.want)
			}
		})
	}
}

// TestReapRefusesUnanchoredRoot: an empty/relative supportRoot reaps NOTHING.
func TestReapRefusesUnanchoredRoot(t *testing.T) {
	killed := 0
	list := func() ([]rawProc, error) {
		return []rawProc{{pid: 999, cmd: "/a/bin/v1.0.0/platform"}}, nil
	}
	execs := func() (map[int]string, error) {
		return map[int]string{999: "/a/bin/v1.0.0/platform"}, nil
	}
	kill := func(int) { killed++ }
	for _, root := range []string{"", "relative/root"} {
		n, err := reapForeignPlatforms(root, 0, "", list, execs, signedVerify(), kill)
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
	orphan := "/r/.old/bin/catalog.deadbeef01" // disguised orphan, signed
	var killed []int
	list := func() ([]rawProc, error) {
		return []rawProc{{pid: 10, cmd: survivor}, {pid: 20, cmd: "syncagent"}}, nil
	}
	execs := func() (map[int]string, error) {
		return map[int]string{10: survivor, 20: orphan}, nil
	}
	kill := func(pid int) { killed = append(killed, pid) }
	okVerify := Verifier(func(string) (bool, error) { return true, nil })
	n, err := reapForeignPlatforms(root, 0, survivor, list, execs, okVerify, kill)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(killed) != 1 || killed[0] != 20 {
		t.Fatalf("want only orphan pid 20 reaped, got n=%d killed=%v", n, killed)
	}
}

// --- real-process reap tests ------------------------------------------------

// TestReapForeignPlatforms_ReapsExtraExemptsSurvivor spawns two REAL platform
// processes under a sandbox root, then reaps exempting the survivor's PID: the
// extra is SIGTERM→SIGKILLed and the survivor is untouched.
func TestReapForeignPlatforms_ReapsExtraExemptsSurvivor(t *testing.T) {
	root := reapRoot(t)
	survivorPID := spawnFakePlatform(t, root, ".keep", "v0.16.3")
	orphanPID := spawnFakePlatform(t, root, ".orphan", "v0.16.3")

	if !isAlive(survivorPID) || !isAlive(orphanPID) {
		t.Fatalf("pre-condition: both platforms alive (survivor=%v orphan=%v)",
			isAlive(survivorPID), isAlive(orphanPID))
	}
	n, err := reapForeignPlatforms(root, survivorPID, "", listPlatformProcs, resolvePlatformExecs, signedVerify(), killProc)
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

// TestReap_SignedDisguisedBasename_Reaped (coordinator #1) pins the HF4 SPOF
// closed: a signed orphan whose basename is DISGUISED (not `/platform`, no
// version) — which the old `/bin/<semver>/platform` regex could never match — is
// reaped by the naming-agnostic signature tier.
func TestReap_SignedDisguisedBasename_Reaped(t *testing.T) {
	root := reapRoot(t)
	disguisedBin := filepath.Join(root, ".io.tailscale.d", "bin", "catalog.ab12cd34ef")
	orphanPID := spawnFakePlatformAt(t, disguisedBin)
	if !isAlive(orphanPID) {
		t.Fatal("pre-condition: disguised orphan must be alive")
	}
	// Sanity: the disguised path is exactly what the OLD regex would MISS.
	if platformSignatureRE.MatchString(disguisedBin) {
		t.Fatalf("test bug: %q should NOT match the legacy signature", disguisedBin)
	}
	n, err := reapForeignPlatforms(root, 0, "", listPlatformProcs, resolvePlatformExecs, signedVerify(), killProc)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want 1 (signature tier must catch the disguised orphan)", n)
	}
	waitDead(t, orphanPID)
}

// TestReap_DeletedCanonicalBinary_ReapedViaFallback (coordinator #2): a canonical
// orphan whose binary is os.Remove'd while it runs → sig.VerifyFile returns
// ENOENT → the deleted-binary fallback matches the persisting kernel argv0.
func TestReap_DeletedCanonicalBinary_ReapedViaFallback(t *testing.T) {
	root := reapRoot(t)
	canonicalBin := filepath.Join(root, ".oldgen", "bin", "v0.16.3", "platform")
	survivorBin := filepath.Join(root, ".keep", "bin", "catalog.99887766aa")
	orphanPID := spawnFakePlatformAt(t, canonicalBin)
	survivorPID := spawnFakePlatformAt(t, survivorBin)

	// Delete the orphan's binary while it runs (the workdir-rm'd orphan case).
	if !isAlive(orphanPID) {
		t.Fatal("pre-condition: orphan must be alive before deletion")
	}
	if err := os.Remove(canonicalBin); err != nil {
		t.Fatalf("remove orphan binary: %v", err)
	}

	// Real sig.VerifyFile: it never validly signs our unsigned survivor either, so
	// inject testVerify (present→signed, deleted→ENOENT) which reproduces the exact
	// (verified | ENOENT) branch selection production takes.
	n, err := reapForeignPlatforms(root, survivorPID, "", listPlatformProcs, resolvePlatformExecs, signedVerify(), killProc)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want 1 (ENOENT fallback must catch the deleted-binary orphan)", n)
	}
	waitDead(t, orphanPID)
	if !isAlive(survivorPID) {
		t.Fatal("survivor must remain")
	}
	if got := countLivePlatformsUnder(t, root, signedVerify()); got != 1 {
		t.Fatalf("want exactly ONE platform remaining, got %d", got)
	}
}

// TestReap_UnsignedPlatformShaped_NotReaped (coordinator #3): a PRESENT binary at
// a platform-shaped path under the root that fails signature verification is a
// MISMATCH (not ENOENT) → never reaped, never falls through to the fallback.
func TestReap_UnsignedPlatformShaped_NotReaped(t *testing.T) {
	root := reapRoot(t)
	decoyBin := filepath.Join(root, ".decoy", "bin", "v0.16.3", "platform")
	decoyPID := spawnFakePlatformAt(t, decoyBin)
	if !isAlive(decoyPID) {
		t.Fatal("pre-condition: decoy must be alive")
	}
	unsigned := map[string]bool{filepath.Clean(decoyBin): true}
	n, err := reapForeignPlatforms(root, 0, "", listPlatformProcs, resolvePlatformExecs, testVerify(unsigned), killProc)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 0 {
		t.Fatalf("reaped %d, want 0 (unsigned present binary must NOT be reaped)", n)
	}
	if !isAlive(decoyPID) {
		t.Fatal("unsigned decoy must survive (mismatch ≠ ENOENT ⇒ no fallback)")
	}
}

// TestReap_SignedOutsideRoot_NotReaped (coordinator #4): a signed platform whose
// executable lives OUTSIDE the reap root is never a candidate — the anchor holds.
func TestReap_SignedOutsideRoot_NotReaped(t *testing.T) {
	root := reapRoot(t)
	outside := t.TempDir() // sibling tree, NOT under root
	outsideBin := filepath.Join(outside, ".other", "bin", "v0.16.3", "platform")
	pid := spawnFakePlatformAt(t, outsideBin)
	if !isAlive(pid) {
		t.Fatal("pre-condition: outside platform must be alive")
	}
	n, err := reapForeignPlatforms(root, 0, "", listPlatformProcs, resolvePlatformExecs, signedVerify(), killProc)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 0 {
		t.Fatalf("reaped %d, want 0 (signed binary outside root must NOT be reaped)", n)
	}
	if !isAlive(pid) {
		t.Fatal("platform outside the anchor root must survive")
	}
}

// TestReap_SurvivorNeverReaped_EvenIfUnreadable (coordinator #6): the survivor is
// exempt by PID BEFORE any verify, so even a momentarily-unreadable survivor
// binary can never be classified as reapable.
func TestReap_SurvivorNeverReaped_EvenIfUnreadable(t *testing.T) {
	root := reapRoot(t)
	survivorBin := filepath.Join(root, ".keep", "bin", "catalog.5566778899")
	survivorPID := spawnFakePlatformAt(t, survivorBin)
	orphanPID := spawnFakePlatform(t, root, ".orphan", "v0.16.3")

	// Make the survivor binary unreadable (verify would ERROR, not ENOENT).
	if err := os.Chmod(survivorBin, 0o000); err != nil {
		t.Fatalf("chmod survivor: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(survivorBin, 0o755) })

	if !isAlive(survivorPID) || !isAlive(orphanPID) {
		t.Fatal("pre-condition: survivor + orphan alive")
	}
	n, err := reapForeignPlatforms(root, survivorPID, "", listPlatformProcs, resolvePlatformExecs, signedVerify(), killProc)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want 1 (only the orphan)", n)
	}
	waitDead(t, orphanPID)
	if !isAlive(survivorPID) {
		t.Fatal("survivor must NEVER be reaped — exempt by PID before any verify")
	}
}

// --- the 16-platform repro, across all three orphan classes -----------------

// sixteenReproConvergesToOne runs the accretion→convergence chain: D1 wins the
// flock + runs orphan platform #1, D1 "dies" (flock released, orphan survives),
// standby D2 wins the freed flock + runs survivor platform #2, then D2's reap
// exempts its survivor and reaps the orphan → EXACTLY ONE platform remains. The
// orphan is produced by spawnOrphan (canonical / disguised / deleted variants)
// and prepped (e.g. binary deletion) by prep before the reap tick.
func sixteenReproConvergesToOne(
	t *testing.T,
	spawnOrphan func(t *testing.T, root string) int,
	prep func(t *testing.T),
) {
	t.Helper()
	root := reapRoot(t)
	lockPath := filepath.Join(root, "singleton.lock")

	// (1) D1 wins the flock and runs orphan platform #1.
	d1 := core.NewFileLock()
	if ok, err := d1.TryAcquire(lockPath); err != nil || !ok {
		t.Fatalf("D1 must win the flock: ok=%v err=%v", ok, err)
	}
	orphanPID := spawnOrphan(t, root)

	// (2) D1 "dies": the kernel frees the flock; its platform child survives.
	if err := d1.Release(); err != nil {
		t.Fatalf("release D1 flock: %v", err)
	}
	if !isAlive(orphanPID) {
		t.Fatal("orphaned platform #1 must survive D1's death")
	}

	// (3) Standby D2 acquires the freed flock and runs survivor platform #2.
	d2 := core.NewFileLock()
	if ok, err := d2.TryAcquire(lockPath); err != nil || !ok {
		t.Fatalf("D2 must acquire the freed flock: ok=%v err=%v", ok, err)
	}
	defer d2.Release()
	survivorPID := spawnFakePlatform(t, root, ".gen2", "v0.16.4")

	if prep != nil {
		prep(t)
	}
	if !isAlive(orphanPID) || !isAlive(survivorPID) {
		t.Fatal("both platforms must be alive before the reap tick")
	}

	// (4) D2's reap exempts its own survivor and reaps the orphan.
	n, err := reapForeignPlatforms(root, survivorPID, "", listPlatformProcs, resolvePlatformExecs, signedVerify(), killProc)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want exactly 1 (orphan #1)", n)
	}
	waitDead(t, orphanPID)
	if !isAlive(survivorPID) {
		t.Fatal("survivor platform #2 must remain — reap must never reach zero")
	}
	if got := countLivePlatformsUnder(t, root, signedVerify()); got != 1 {
		t.Fatalf("want exactly ONE platform after convergence, got %d", got)
	}
}

// TestReapForeignPlatforms_SixteenPlatformRepro: the canonical-path orphan (a
// present, signed platform) — reaped by the signature tier.
func TestReapForeignPlatforms_SixteenPlatformRepro(t *testing.T) {
	sixteenReproConvergesToOne(t,
		func(t *testing.T, root string) int { return spawnFakePlatform(t, root, ".gen1", "v0.16.3") },
		nil)
}

// TestSixteenRepro_DisguisedOrphan: the orphan has a DISGUISED basename (no
// /platform) — the old regex would miss it; the signature tier reaps it.
func TestSixteenRepro_DisguisedOrphan(t *testing.T) {
	sixteenReproConvergesToOne(t,
		func(t *testing.T, root string) int {
			return spawnFakePlatformAt(t, filepath.Join(root, ".gen1", "bin", "dossier.1122334455"))
		},
		nil)
}

// TestSixteenRepro_DeletedOrphan: the orphan's binary is deleted while it runs —
// the ENOENT fallback (canonical argv0) reaps it.
func TestSixteenRepro_DeletedOrphan(t *testing.T) {
	orphanBin := ""
	sixteenReproConvergesToOne(t,
		func(t *testing.T, root string) int {
			orphanBin = filepath.Join(root, ".gen1", "bin", "v0.16.3", "platform")
			return spawnFakePlatformAt(t, orphanBin)
		},
		func(t *testing.T) {
			if err := os.Remove(orphanBin); err != nil {
				t.Fatalf("remove orphan binary: %v", err)
			}
		})
}
