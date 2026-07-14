//go:build darwin

package osadapter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/platdir"
	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

// FEATURE 25 — install/upgrade converges to EXACTLY ONE daemon + one platform.
//
// The old install path retired only spec.Mode's generations, so a sudo (system)
// install never retired the user generations (and vice-versa) — each mode kept
// running its own daemon + platform. ConvergeSingleInstance closes that hole:
// it retires every OTHER generation across BOTH the user and system domains,
// kills each retired generation's platform, reaps orphaned platform processes,
// and sweeps stale daemon-home + platform-workdirs — all best-effort so a
// convergence step can NEVER fail the install.
//
// Cross-mode privilege: a sudo/system install CAN bootout the other (gui) domain
// → full convergence. A user install converges its own domain fully and reaps
// its own platforms; the system-domain teardown steps EPERM and are silently
// skipped (count-only, continue), deferring system cleanup to a later sudo run.
//
// keepMode/keepDaemonBin identify the survivor: any generation whose daemon
// binary differs from keepDaemonBin is retired. keepPlatformBin/keepPlatformPID
// exempt the survivor's platform from the reap (PID when known; path covers the
// install-time window before the survivor platform is running).
func ConvergeSingleInstance(
	keepMode mode.Mode, keepDaemonBin, keepPlatformBin string, keepPlatformPID int,
) (retired, reaped int, err error) {
	if keepDaemonBin == "" {
		// An empty keep would make EVERY generation "other" → tear the whole
		// install down. A caller with no survivor is a bug, not a wipe request.
		return 0, 0, fmt.Errorf("converge: keepDaemonBin must not be empty")
	}
	home, _ := os.UserHomeDir()
	keepDaemonHome := filepath.Dir(keepDaemonBin)
	// The survivor's platform-workdir (the disposable engine storage) is recorded
	// in a pointer inside daemon-home; read it so the stale-platform sweep keeps
	// the live one.
	keepPlatformWorkdir := platdir.Read(keepDaemonHome)

	// FEATURE 25: the SIGNAL/DELETE-of-live-processes steps (killGenerationPlatform
	// during retire, and the reapForeignPlatforms sweep) are gated OFF for test
	// mode. A test convergence therefore does ONLY the same launchd/disk retire +
	// sweep the pre-F25 code already did (all HOME-anchored to the e2e sandbox) —
	// F25 adds ZERO new process-killing capability to the test path, so a
	// forgotten HOME override can never turn a test run into a real-process kill.
	killPlat := killGenerationPlatform // real: resolve + pkill the platform
	if keepMode == mode.Test {
		killPlat = func(string) func(string) { return nil } // no-op: no process kills in test
	}

	for _, m := range convergeModes(keepMode, os.Geteuid()) {
		root := mode.SupportRoot(m, home)

		// Retire OTHER generations in this domain (daemon binary != keep). A
		// discovery failure here (e.g. EPERM reading the other domain) is
		// best-effort: skip retirement for this domain and keep converging.
		if gens, dead, derr := DiscoverAllGenerations(m, sig.VerifyFile); derr == nil {
			c := launchctlCtl{m: m}
			retired += retireGenerations(gens, dead, keepDaemonBin, root,
				c.bootout, os.Remove, pkillBinary, killPlat(root), os.RemoveAll)
		}

		// Disk-side sweeps. In the OTHER domain nothing equals the keep, so the
		// whole other-domain residue is swept; in keepMode the survivor's dirs
		// are preserved. Best-effort (errors ignored).
		_, _ = SweepOrphanWorkdirs(m, keepDaemonHome)
		_, _ = SweepStalePlatformWorkdirs(root, keepPlatformWorkdir)

		// Reap orphaned platform PROCESSES in this domain (real installs only —
		// see the test gate above). The survivor's platform (keepMode only) is
		// exempt by PID and by path.
		if keepMode == mode.Test {
			continue
		}
		exemptPID, exemptPath := 0, ""
		if m == keepMode {
			exemptPID, exemptPath = keepPlatformPID, keepPlatformBin
		}
		if n, rerr := reapForeignPlatforms(root, exemptPID, exemptPath, listPlatformProcs, killProc); rerr == nil {
			reaped += n
		}
	}
	return retired, reaped, nil
}

// convergeModes returns the domains a convergence spans.
//   - Test → ONLY the throwaway sandbox domain. It must NEVER reach into the real
//     user (~/Library) or system (/Library) roots (mode.System resolves to
//     /Library unconditionally), so Test is scoped strictly to itself.
//   - root (euid 0, a sudo/system install) → BOTH domains: it can bootout the
//     gui domain and delete under both roots, so it fully converges user+system.
//   - non-root (a plain user install) → ONLY the user domain. It has no privilege
//     to bootout/delete the system domain, so attempting it would be a guaranteed
//     EPERM no-op; we DEFER system cleanup to a later sudo run instead of poking
//     /Library at all (defense-in-depth — never rely on directory ACLs alone).
func convergeModes(keepMode mode.Mode, euid int) []mode.Mode {
	if keepMode == mode.Test {
		return []mode.Mode{mode.Test}
	}
	if euid == 0 {
		return []mode.Mode{mode.User, mode.System}
	}
	return []mode.Mode{mode.User}
}

// killGenerationPlatform returns the retireGenerations platform-kill seam for a
// domain: given a retired generation's daemon-home, resolve its platform-workdir
// via the pointer and best-effort pkill the platform process running out of it.
// GUARDED — the platform-workdir must be a long, absolute path strictly under
// this domain's support root before it is handed to `pkill -f`, so a short/blank
// pointer can never widen the blast radius. A gone daemon-home (dead generation)
// yields "" ⇒ no-op; the reap step still catches its orphan by signature.
func killGenerationPlatform(supportRoot string) func(daemonHome string) {
	return func(daemonHome string) {
		pw := platdir.Read(daemonHome)
		if len(pw) <= minBinPathLen || !pathStrictlyUnder(pw, supportRoot) {
			return
		}
		// The platform's argv is `<pw>/bin/<v>/platform --workdir <pw>`, so
		// `pkill -f <pw>` matches it (and only it — the daemon binary lives under
		// the SEPARATE daemon-home and never carries <pw> in its argv).
		//
		// NOTE: pkill -f treats <pw> as a BRE. <pw> is a focusd-generated disguise
		// path (relocate.HiddenWorkdir: a dot-prefixed alphanumeric name); the only
		// metachar it contains is `.`, which as a wildcard only ever WIDENS toward
		// other chars at that exact position in an already-long, highly-specific
		// path — it cannot broaden the match to an unrelated process. Same property
		// as the existing pkillBinary. Keep disguise names metachar-free beyond `.`.
		_ = exec.Command("pkill", "-f", pw).Run()
	}
}

// pathStrictlyUnder reports whether path is an absolute path strictly nested
// under root (never root itself, never outside it). A lighter lexical check than
// safeToRemoveWorkdir (no RemoveAll here — only a pkill match), but enough to
// keep the pkill pattern anchored to this domain's tree.
func pathStrictlyUnder(path, root string) bool {
	if path == "" || root == "" || !filepath.IsAbs(path) || !filepath.IsAbs(root) {
		return false
	}
	clean := filepath.Clean(path)
	base := filepath.Clean(root) + string(filepath.Separator)
	return strings.HasPrefix(clean, base)
}
