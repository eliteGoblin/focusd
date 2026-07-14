//go:build darwin

package osadapter

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// FEATURE 25 — continuous single-platform convergence (the DOMINANT reap).
//
// The daemon flock ELECTS one platform supervisor but never REAPS extras: on a
// daemon death the platform child reparents to launchd and SURVIVES; a standby
// daemon then acquires the freed flock and starts a SECOND platform. Every
// crash/self-update cycle adds one, so the machine accretes orphaned platforms.
// ReapForeignPlatforms is the reaper the flock lacked: the lock WINNER
// enumerates running platform processes and SIGTERM→SIGKILLs every one that is
// NOT the survivor it exempts.
//
// SAFETY: a process is a reap candidate ONLY when BOTH anchors hold — its
// argv[0] is (1) strictly under THIS mode's Application-Support root AND (2)
// ends in the platform signature `/bin/<semver>/platform`. Either alone is
// insufficient; together they can never match a non-focusd process. The reaper
// returns a COUNT only and never logs the matched path.
//
// HF4 NOTE (disguise): platformSignatureRE is THE SINGLE POINT to update if the
// platform process identity ever changes. HF4 may relocate/rename the platform
// binary; if the running platform's executable path no longer matches this
// pattern the reaper silently matches nothing (fails safe, never over-matches).
// Keep this regex in lockstep with how the platform binary is laid out (today:
// core.Store.BinPath ⇒ <platform-workdir>/bin/<version>/platform).
//
// It is `$`-anchored to the END of the executable path so a sibling binary in
// the same versioned dir (e.g. `.../bin/v1.2.3/platform-debug`) can NEVER be
// misclassified as the platform — the match is the whole basename `platform`,
// not a prefix of it.
var platformSignatureRE = regexp.MustCompile(
	`/bin/v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.\-]+)?(\+[0-9A-Za-z.\-]+)?/platform$`)

// rawProc is one enumerated process: its pid and executable path (argv[0], from
// `ps comm=`). The path is NOT the full command line — using the executable-only
// column means the signature can never accidentally match text inside a later
// ARGUMENT, only the binary itself. The path may contain spaces ("Application
// Support"), so callers must not space-split it.
type rawProc struct {
	pid int
	cmd string
}

// procLister enumerates running processes. Real impl shells to `ps`; tests
// inject a fake table so the classification + exemption logic is exercised
// without spawning processes.
type procLister func() ([]rawProc, error)

// procKiller SIGTERM→SIGKILLs a pid. Real impl signals the process; tests
// record the pids asked to die.
type procKiller func(pid int)

// ReapForeignPlatforms SIGTERM→SIGKILLs every running platform process anchored
// under THIS mode's Application-Support root that is NOT keepPID. It is the
// exported entry the reconcile-loop winner and self-update wire to. Count-only
// return; the matched path is never surfaced. keepPID<=0 means "no PID
// exemption" (used at install time before any survivor is running); the reaper
// is still structurally incapable of reaching zero live platforms because the
// daemon that calls it always keeps + restarts its own survivor.
func ReapForeignPlatforms(keepPID int) (killed int, err error) {
	home, _ := os.UserHomeDir()
	root := mode.SupportRoot(mode.Resolve(), home)
	return reapForeignPlatforms(root, keepPID, "", listPlatformProcs, killProc)
}

// reapForeignPlatforms is the pure, seam-injected core. supportRoot is the
// anchor (a foreign platform MUST live strictly under it); keepPID and keepPath
// are the twin exemptions for the survivor (path covers the window where the
// survivor PID is not yet known, e.g. at install/convergence time).
func reapForeignPlatforms(
	supportRoot string, keepPID int, keepPath string,
	list procLister, kill procKiller,
) (int, error) {
	// A non-absolute / empty root cannot anchor the signature safely — refuse to
	// reap ANYTHING rather than risk an unanchored match. (Mirrors the
	// safeToRemoveWorkdir belt: no anchor ⇒ no action.)
	if supportRoot == "" || !filepath.IsAbs(supportRoot) {
		return 0, nil
	}
	procs, err := list()
	if err != nil {
		return 0, err
	}
	keepClean := ""
	if keepPath != "" {
		keepClean = filepath.Clean(keepPath)
	}
	killed := 0
	for _, p := range procs {
		if p.pid <= 0 || p.pid == keepPID {
			continue
		}
		argv0, ok := classifyPlatformArgv(p.cmd, supportRoot)
		if !ok {
			continue
		}
		if keepClean != "" && filepath.Clean(argv0) == keepClean {
			continue // the survivor, exempt by path when its PID isn't known yet
		}
		kill(p.pid)
		killed++
	}
	return killed, nil
}

// classifyPlatformArgv reports whether execPath (a process's argv[0]/comm) is a
// focusd platform binary strictly under supportRoot. It anchors on BOTH the
// `$`-terminated signature AND the support-root prefix, so it can never match a
// non-focusd process — and because execPath is the executable path only (no
// arguments), the signature can never be spoofed by a later argument. Pure →
// unit-tested.
func classifyPlatformArgv(execPath, supportRoot string) (path string, ok bool) {
	if !filepath.IsAbs(execPath) {
		return "", false // relative/basename-only comm → cannot anchor → not ours
	}
	if !platformSignatureRE.MatchString(execPath) {
		return "", false // not the /bin/<semver>/platform binary
	}
	clean := filepath.Clean(execPath)
	root := filepath.Clean(supportRoot) + string(filepath.Separator)
	if !strings.HasPrefix(clean, root) {
		return "", false // signature present but NOT under this mode's root
	}
	return execPath, true
}

// listPlatformProcs enumerates every process as (pid, executable path) via `ps`.
// `-axww` = all processes, unlimited width (so a long disguised path is not
// truncated); `-o pid=,comm=` = no header, pid then the EXECUTABLE PATH ONLY
// (macOS `comm` is the full argv[0] path, not truncated like Linux). Using comm
// (not command) means a process's ARGUMENTS never enter the signature match.
func listPlatformProcs() ([]rawProc, error) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,comm=").Output()
	if err != nil {
		return nil, err
	}
	var procs []rawProc
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sp := strings.IndexByte(line, ' ')
		if sp < 0 {
			continue // pid with no exec path — skip
		}
		pid, perr := strconv.Atoi(line[:sp])
		if perr != nil {
			continue
		}
		execPath := strings.TrimSpace(line[sp+1:])
		procs = append(procs, rawProc{pid: pid, cmd: execPath})
	}
	return procs, nil
}

// killProc delivers SIGTERM then SIGKILL back-to-back with NO grace interval —
// this is an immediate kill, not a graceful drain. An orphaned platform we are
// reaping holds only disposable state (nothing to flush), and a per-pid grace
// window would stall the reconcile tick when several orphans have accreted. The
// SIGTERM is sent first only so a process that installs a fast terminate handler
// can exit cleanly; SIGKILL immediately after guarantees death regardless.
func killProc(pid int) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Signal(syscall.SIGTERM)
	_ = proc.Signal(syscall.SIGKILL)
}
