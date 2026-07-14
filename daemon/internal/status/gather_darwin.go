//go:build darwin

package status

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/osadapter"
	"github.com/eliteGoblin/focusd/daemon/internal/platdir"
	"github.com/eliteGoblin/focusd/daemon/internal/sig"
	"github.com/eliteGoblin/focusd/daemon/internal/status/redact"
)

// sigVerifier is the signature-check seam for the platform binary about to be
// exec'd (go-review HIGH). Production wires sig.VerifyFile (real Ed25519 against
// the embedded key); tests inject a fake because the offline signing key is not
// available in CI. Threaded as a param (never a package global) so parallel
// tests can't race on it — mirroring osadapter.Verifier.
type sigVerifier func(path string) (bool, error)

// platformStore builds a Store rooted at the discovered daemon-home whose
// platform binaries resolve to the disposable platform-workdir (FEATURE 21 /
// HF1), read from the pointer file. Status only READS the pointer — it must
// never create a platform-workdir — so a missing pointer falls back to the
// daemon-home (legacy single-root). Returns the store and the platform-workdir
// path (both derived from the already-tokenised daemon-home; neither escapes
// the caller's redact.Use closure).
func platformStore(daemonHome string) (*core.Store, string) {
	platWD := platdir.Read(daemonHome)
	// go-review HIGH: the pointer target is attacker-writable and flows into an
	// exec below, so status must not trust it blindly. Run it through the
	// read-only containment guard (platdir.SafeTarget) and fall back to the
	// daemon-home (legacy single-root) on ANY failure. supportRoot is the
	// daemon-home's parent — a hidden daemon-home sits directly under the mode's
	// Application Support root, so its parent IS that root. Status NEVER creates
	// a platform-workdir (read-only), so an unsafe pointer simply degrades to
	// reading the daemon-home rather than steering an exec off a hostile path.
	if platWD == "" || !platdir.SafeTarget(platWD, filepath.Dir(daemonHome), daemonHome) {
		platWD = daemonHome
	}
	st := &core.Store{Dir: daemonHome}
	if platWD != daemonHome {
		st.PlatformDir = platWD
	}
	return st, platWD
}

// warmupWindow is how young an install (by version.json mtime) may be, with
// no good version yet, and still read HEALTHY — warming up rather than DOWN.
const warmupWindow = 10 * time.Minute

// platformStatusTimeout caps the delegated `platform status` exec. On expiry
// the child is killed and platform detail is marked unavailable — the daemon
// still reports its own facts.
const platformStatusTimeout = 8 * time.Second

// Gather builds the daemon Snapshot and captures the delegated platform
// detail. This is the ONLY place that touches exec / the filesystem / raw
// disguised paths. Every disguised value (workdir, binary path) is confined
// to a redact.Token and only materialised inside a redact.Use closure to
// build an exec arg or a pgrep pattern — it never enters the Snapshot, an
// error string, or any rendered output.
//
// workdirOverride, when non-empty, is an operator-supplied workdir (a CLI
// flag, not a discovered disguised value); it takes the place of discovery.
//
// jsonMode selects which single `platform status` form to exec: the JSON
// report (true) or the text render (false). Exactly ONE is run per daemon
// invocation — the one the daemon will actually render — so the delegated
// child is never executed twice (no doubled timeout budget, no two snapshots).
func Gather(workdirOverride string, jsonMode bool) (Snapshot, PlatformDetail) {
	m := mode.Resolve()
	s := Snapshot{Mode: string(m)}

	// --- Mesh roles (counts only cross the osadapter boundary) ---
	loaded, total, found, err := osadapter.MeshStatus(m)
	if err != nil {
		// A probe failure (permission-denied read of a root-owned system
		// install queried without sudo, or any other IO error at this seam)
		// is bucketed to "unknown" — NEVER reported as DOWN. We can't tell
		// EACCES from a genuine no-install at this boundary, so we always
		// prefer the honest "unknown" over falsely calling a probe failure
		// a down engine.
		s.MeshUnknown = true
	} else {
		s.MeshLoaded = loaded
		s.MeshTotal = total
		s.Found = found
	}

	// --- Out-of-band watchdog rail liveness (bools only) ---
	// FEATURE 12 / ADR-0016: a silently-dead cron watchdog must be checkable.
	// WatchdogStatus crosses ONLY bools (cron line present / copy on disk) —
	// the copy path never leaves the osadapter boundary, so this cannot leak.
	cronPresent, copyOK := osadapter.WatchdogStatus(m)
	s.WatchdogChecked = true
	s.WatchdogCron = cronPresent
	s.WatchdogCopyOK = copyOK

	// --- Discover the install (workdir + binary path stay tokenised) ---
	var workdirTok redact.Token
	if workdirOverride != "" {
		workdirTok = redact.New(workdirOverride)
		s.Found = true
	} else {
		cur, ferr := osadapter.FindCurrentInstall(m, sig.VerifyFile)
		if ferr != nil {
			// Discovery IO failure on a system install without root → unknown.
			s.VersionsUnknown = true
		} else if cur.Workdir != "" {
			workdirTok = redact.New(cur.Workdir)
			s.Found = s.Found || cur.BinaryPath != ""
		}
	}

	// --- Versions from the store (read inside Use so the path stays hidden) ---
	if workdirTok.Present() && !s.VersionsUnknown {
		desired, good, vUnknown := readVersions(workdirTok)
		s.Desired = desired
		s.Good = good
		s.VersionsUnknown = vUnknown

		// Warming up: no good version yet AND install is younger than the
		// warmup window (derive age from version.json mtime, inside Use).
		if good == "" && !vUnknown {
			if age, ok := installAge(workdirTok); ok && age < warmupWindow {
				s.WarmingUp = true
			}
		}

		// Process count: match the EXACT good-binary path. Built + run inside
		// Use so the path (the secret) never escapes. Only meaningful when a
		// good version exists.
		if good != "" {
			s.ProcCount = procCount(workdirTok, good)
		}
	} else if !workdirTok.Present() && !s.VersionsUnknown && s.Found {
		// Found a mesh but couldn't recover a workdir → versions unknown.
		s.VersionsUnknown = true
	}

	// --- Delegate plugin detail to `platform status` ---
	pd := gatherPlatform(workdirTok, jsonMode, sig.VerifyFile)
	s.PlatformUnavailable = !pd.Available

	return s, pd
}

// readVersions reads desired + good from the store under the tokenised
// workdir. vUnknown=true when the workdir is unreadable (permission/absent) —
// distinct from "readable but no good promoted yet" (good=="").
func readVersions(workdir redact.Token) (desired, good string, vUnknown bool) {
	type versions struct {
		desired, good string
		vUnknown      bool
	}
	v := redact.Use(workdir, func(raw string) versions {
		// Distinguish EACCES (unknown) from a readable workdir with no good
		// file (genuine state). Stat the dir: if we can't even read it, unknown.
		if fi, err := os.Stat(raw); err != nil || !fi.IsDir() {
			if os.IsPermission(err) {
				return versions{vUnknown: true}
			}
			// Absent workdir: nothing to report, not an error.
			return versions{}
		}
		st := &core.Store{Dir: raw}
		return versions{desired: st.Desired(), good: st.Good()}
	})
	return v.desired, v.good, v.vUnknown
}

// installAge returns how long ago version.json was last written, used to tell
// "warming up" from "down". The path stays inside the Use closure.
func installAge(workdir redact.Token) (time.Duration, bool) {
	return redactUse2(workdir, func(raw string) (time.Duration, bool) {
		fi, err := os.Stat(filepath.Join(raw, core.VersionFile))
		if err != nil {
			return 0, false
		}
		return time.Since(fi.ModTime()), true
	})
}

// procCount counts live platform processes. The salt-independent pidfile (P3)
// is the PRIMARY up/down signal; pgrep is the fallback AND the orphan counter.
// Everything runs inside the Use closure so the disguised path never escapes.
//
// HF4 (P3): a pgrep pattern is reconstructed from the per-install salt to match
// the disguised child's argv EXACTLY (-f -x). But if the salt on disk diverged
// from the running child's argv (the F1 race — now fixed, but status must stay
// correct regardless), that pattern MISSES and pgrep returns 0. So a live,
// still-supervised child confirmed by the pidfile floors the count at 1 — a
// pgrep miss can never falsely report DOWN. pgrep still runs, so a genuine >1
// (orphan) anomaly (ADR-0013) is preserved: we take max(pgrep, pidfile), never
// let the pidfile MASK an orphan nor let a pgrep miss MASK a live child.
func procCount(workdir redact.Token, good string) int {
	return redact.Use(workdir, func(raw string) int {
		st, platWD := platformStore(raw)
		// pgrep -f -x: match the full argument vector EXACTLY.
		//
		// HF4 (FEATURE 24): the disguised platform child runs as `<argv0> run`
		// (a generic token, workdir off argv/env), so match that deterministic
		// argv. Legacy (no salt) installs still run as `<binPath> --workdir
		// <platform-workdir>` (FEATURE 21 / HF1).
		var pattern string
		if argv0 := st.PlatformArgv0(); argv0 != "" {
			pattern = argv0 + " run"
		} else {
			pattern = st.BinPath(good) + " --workdir " + platWD
		}
		n := pgrepCountExact(pattern)
		// The pidfile is the salt-independent primary signal. Floor at 1 when it
		// confirms a live supervised child, but NEVER below the pgrep count (so
		// an orphan anomaly still surfaces).
		if n < 1 && platformPidUp(st.Dir) {
			return 1
		}
		return n
	})
}

// pgrepCountExact returns how many live processes have argv EXACTLY equal to
// pattern (`pgrep -f -x`). Any error — including exit 1 (no match) — counts as 0
// rather than leaking the pattern through an error string. Time-boxed like the
// other status probes.
func pgrepCountExact(pattern string) int {
	ctx, cancel := context.WithTimeout(context.Background(), platformStatusTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pgrep", "-f", "-x", pattern).Output()
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range bytes.Split(bytes.TrimSpace(out), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			n++
		}
	}
	return n
}

// platformPidUp reports whether the daemon-home pidfile names a live, still-
// SUPERVISED platform child: the pid is alive AND not reparented to launchd
// (ppid != 1). It is the salt-INDEPENDENT primary liveness signal — a bare int,
// so it survives a salt divergence that would desync the pgrep argv pattern.
// Returns false ("no usable signal") when the pidfile is missing/stale or the
// child is orphaned, so the caller falls back to pgrep (which also counts the
// orphan). The path is never surfaced.
func platformPidUp(daemonHome string) bool {
	b, err := os.ReadFile(filepath.Join(daemonHome, core.PlatformPidFile))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false
	}
	if !processAlive(pid) {
		return false
	}
	ppid, ok := processPpid(pid)
	// ppid unknown (proc vanished mid-probe) or reparented to launchd (== 1) →
	// no positive signal; the pgrep fallback + FEATURE 25 reaper handle orphans.
	return ok && ppid != 1
}

// processAlive reports whether pid names a live process (signal-0 probe). EPERM
// means the process exists but is owned by another user (e.g. a root platform
// queried by a non-root `status`) — still alive.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// processPpid returns pid's parent pid via sysctl (kern.proc.pid). ok=false when
// the process is gone or the read fails.
func processPpid(pid int) (int, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil {
		return 0, false
	}
	return int(kp.Eproc.Ppid), true
}

// gatherPlatform execs `platform status` EXACTLY ONCE, in the single form the
// daemon will render: `--json` when jsonMode, otherwise text. Only the needed
// output is produced — no doubled exec, no two divergent snapshots. On timeout
// / non-zero exit / exec error it returns an unavailable detail — the daemon
// never fails on this. Platform STDERR is SWALLOWED (never forwarded, never
// interpolated into any error): it can contain disguised paths the platform
// logs about itself.
func gatherPlatform(workdir redact.Token, jsonMode bool, verify sigVerifier) PlatformDetail {
	if !workdir.Present() {
		return PlatformDetail{Available: false}
	}
	// Run the good-version platform binary in `status` mode with the
	// discovered workdir. The binary path and the workdir both stay inside
	// the Use closure — they never escape into the returned detail.
	pd := redact.Use(workdir, func(raw string) PlatformDetail {
		st, platWD := platformStore(raw)
		good := st.Good()
		if good == "" {
			// No good version → no platform process to query.
			return PlatformDetail{Available: false}
		}
		binPath := st.BinPath(good)
		// FEATURE 21 (HF1): query the platform in its own disposable workdir
		// (state.db lives there), not the daemon-home.
		//
		// HF4 (P2): with a DISGUISED install the workdir must NOT ride on argv —
		// a transient `platform status --workdir <wd>` would leak it to `ps` for
		// the ~8s of the call. Pass "" so the disguised child self-derives its
		// workdir from its own binary location, mirroring how it was launched.
		// Legacy/e2e (no salt ⇒ PlatformArgv0()=="") still passes --workdir: that
		// binary is built with -tags e2e and its bin/<v>/platform layout is NOT
		// what the child's 2-levels-up self-derive resolves.
		statusWD := platWD
		if st.PlatformArgv0() != "" {
			statusWD = ""
		}
		out, exitCode, ran := runPlatformStatus(binPath, statusWD, jsonMode, verify)

		// The command itself failed to produce a health verdict (exec error,
		// timeout, or exit >= 2) → unavailable. The daemon still reports its
		// own facts; this is a note, never a forced failure.
		if !ran {
			return PlatformDetail{Available: false}
		}

		if jsonMode {
			// A health verdict but EMPTY/INVALID json is not embeddable →
			// unavailable (we cannot trust an empty/garbage report).
			if out == "" || !json.Valid([]byte(out)) {
				return PlatformDetail{Available: false}
			}
			return PlatformDetail{
				Available: true,
				ExitCode:  exitCode,
				JSON:      json.RawMessage(out),
			}
		}
		// Text mode: empty output despite a verdict → unavailable.
		if out == "" {
			return PlatformDetail{Available: false}
		}
		return PlatformDetail{
			Available:  true,
			ExitCode:   exitCode,
			TextOutput: out,
		}
	})
	return pd
}

// runPlatformStatus execs `<binPath> status [--workdir <wd>] [--json]` with an
// 8s timeout and captures stdout into a buffer. HF4 (P2): --workdir is passed
// ONLY when workdir != "" (legacy/e2e); a disguised install passes "" so the
// child self-derives its workdir and nothing leaks to `ps`. STDERR is discarded
// (it may carry disguised paths). On timeout the child is killed via context
// cancellation.
//
// `platform status` is a HEALTH probe: it exits 0 (healthy/unknown) OR 1
// (degraded) and STILL produces valid, useful output in both cases. So a
// degraded platform (exit 1) ran SUCCESSFULLY — we must not treat it as
// unavailable and hide its real degradation. ran=true means the command
// genuinely produced a verdict (exit 0 or 1); exitCode carries which one so
// the caller can fold the platform verdict into the daemon's overall.
//
// ran=false (=> unavailable) only on: exec error, context timeout, or any exit
// code >= 2 (an internal-error/usage failure of the platform itself, not a
// health verdict). Empty/invalid output is judged by the caller per mode.
func runPlatformStatus(binPath, workdir string, asJSON bool, verify sigVerifier) (out string, exitCode int, ran bool) {
	// go-review HIGH: binPath is resolved via the (attacker-writable) pointer
	// file, so verify its Ed25519 signature against the embedded key BEFORE
	// executing it. A tampered/planted binary at the pointed path fails the
	// check and is treated as unavailable — never run. sig.VerifyFile also
	// errors (→ unavailable) when the file is absent/unreadable. verify is a
	// seam (production: sig.VerifyFile; tests inject a fake, since the offline
	// signing key is not in CI) — mirrors osadapter.Verifier, NOT a package
	// global, so parallel tests can't race on it.
	if ok, verr := verify(binPath); verr != nil || !ok {
		return "", 0, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), platformStatusTimeout)
	defer cancel()

	args := []string{"status", "--no-color"}
	if workdir != "" {
		args = append(args, "--workdir", workdir)
	}
	if asJSON {
		args = append(args, "--json")
	}
	cmd := exec.CommandContext(ctx, binPath, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil // swallow: platform stderr can contain disguised paths

	err := cmd.Run()
	// Context timeout: the child was killed → genuinely unavailable.
	if ctx.Err() != nil {
		return "", 0, false
	}
	if err == nil {
		return stdout.String(), 0, true // clean exit 0
	}
	// Non-zero exit: only exit 1 (DEGRADED) is still a valid health verdict.
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code := ee.ExitCode()
		if code == 1 {
			return stdout.String(), 1, true
		}
		// code >= 2 (or -1 on signal) => platform internal/usage failure.
		return "", code, false
	}
	// Exec error (binary missing, not startable) => unavailable.
	return "", 0, false
}

// redactUse2 is a two-return variant of redact.Use. redact.Use is generic
// over a single return; the gather needs pairs (text+json, age+ok), so we
// thread them through a struct-free closure here while keeping the raw value
// confined to the closure exactly as redact.Use does.
func redactUse2[A any, B any](t redact.Token, fn func(raw string) (A, B)) (A, B) {
	type pair struct {
		a A
		b B
	}
	p := redact.Use(t, func(raw string) pair {
		a, b := fn(raw)
		return pair{a, b}
	})
	return p.a, p.b
}
