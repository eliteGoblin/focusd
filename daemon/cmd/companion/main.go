// Command companion is the focusd out-of-band recovery rail (FEATURE 18 /
// ADR-0020). It is a SEPARATE, minimal binary living in its own fixed disguised
// folder (NOT the daemon binary, NOT under the daemon's workdir), run by launchd
// on a StartInterval. Each run performs one recovery pass: if the daemon's
// heartbeat has gone stale, it promotes its own signature-verified offline copy
// of the daemon and hands off to the daemon's idempotent `watchdog` rebuild —
// restoring protection with NO network. Then it exits; launchd re-runs it.
//
// Minimal by design (ADR-0020): few reasons to change, so the rail stays stable
// and out of the way. Its dependencies are deliberately tiny — mode, sig, and
// internal/companion — and it does NOT import the daemon's osadapter install
// code. launchd, not cron, so it can be established + repaired in an automated
// context WITHOUT Full Disk Access (the failure ADR-0020 reverses).
package main

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/companion"
	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

func main() { os.Exit(run()) }

func run() int {
	// Issue #101: SELF-DERIVE the companion folder from our OWN binary path — NOT
	// from $HOME or mode. All companion files are siblings of this binary under
	// Dir.root, and Dir.root == filepath.Dir(os.Executable()) in both user and
	// system installs (the daemon placed this binary under the mode-correct root).
	// A system LaunchDaemon runs with NO $HOME, so the old os.UserHomeDir() call
	// errored EVERY interval and returned BEFORE reaching recover() — leaving the
	// sole out-of-band recovery rail permanently dead. We now refuse ONLY if we
	// cannot resolve our own executable (which would make every sibling path
	// wrong); mode/home no longer enter the picture.
	exe, err := os.Executable()
	if err != nil {
		// PATH-FREE: never print identifying paths; launchd re-runs us next interval.
		os.Stderr.WriteString("companion: cannot resolve own executable path\n")
		return 1
	}
	dir := companion.DirFromBinary(exe)
	if rerr := recover(dir, time.Now(), sig.VerifyFile, execWatchdog); rerr != nil {
		// PATH-FREE: never print the disguised companion/daemon paths a
		// weak-moment self would need. Keep it abstract; launchd captures this
		// to the companion log.
		os.Stderr.WriteString("companion: recovery pass did not complete\n")
		return 1
	}
	return 0
}

// watchdogExecTimeout bounds the companion's blocking watchdog handoff (#106-b3).
// A genuinely hung `daemon watchdog` would otherwise keep the companion one-shot
// alive forever — launchd never fires a fresh pass (the wedged-rail class #106-b2
// heals daemon-side), and the rail's own cadence stalls. A few minutes is well above
// the ~1-minute worst-case healthy rebuild, so it never cuts off a legitimate run;
// past it, the process is killed and the one-shot exits, freeing launchd to re-run
// the companion next interval.
const watchdogExecTimeout = 3 * time.Minute

// execWatchdog runs the promoted daemon binary's idempotent watchdog rebuild:
//
//	<bin> watchdog -v <desired>
//
// The watchdog no-ops when the mesh is already complete (the anti-fight guard),
// so a false-positive staleness costs at most one harmless run. A child run +
// wait is enough; launchd re-runs the companion on the next interval. Bounded by
// watchdogExecTimeout (#106-b3) so a hung rebuild can't wedge the one-shot forever.
func execWatchdog(bin, desired string) error {
	return execWatchdogCtx(context.Background(), bin, desired, watchdogExecTimeout)
}

// execWatchdogCtx is the timeout-bounded core of execWatchdog, split out with an
// explicit context + timeout so the kill-on-timeout behavior is unit-tested without
// waiting minutes. On timeout, exec.CommandContext SIGKILLs the child and Run
// returns a non-nil error.
func execWatchdogCtx(ctx context.Context, bin, desired string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(ctx, bin, "watchdog", "-v", desired).Run()
}
