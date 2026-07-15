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

// execWatchdog runs the promoted daemon binary's idempotent watchdog rebuild:
//
//	<bin> watchdog -v <desired>
//
// The watchdog no-ops when the mesh is already complete (the anti-fight guard),
// so a false-positive staleness costs at most one harmless run. A child run +
// wait is enough; launchd re-runs the companion on the next interval.
func execWatchdog(bin, desired string) error {
	return exec.Command(bin, "watchdog", "-v", desired).Run()
}
