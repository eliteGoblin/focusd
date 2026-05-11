package daemon

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
	"github.com/eliteGoblin/focusd/app_mon/internal/infra"
)

// StartDaemon spawns a new daemon process with an obfuscated name.
// The daemon is detached from the parent process (runs independently).
// Uses the installed binary path from ExecModeConfig, not os.Executable(),
// to ensure daemons run from the expected install location.
func StartDaemon(role domain.DaemonRole) error {
	return StartDaemonWithPathAndMode(role, "", "")
}

// StartDaemonWithMode spawns a daemon with explicit mode override.
func StartDaemonWithMode(role domain.DaemonRole, mode string) error {
	return StartDaemonWithPathAndMode(role, "", mode)
}

// StartDaemonWithPath spawns a daemon from a specific binary path (deprecated, use StartDaemonWithPathAndMode).
func StartDaemonWithPath(role domain.DaemonRole, binaryPath string) error {
	return StartDaemonWithPathAndMode(role, binaryPath, "")
}

// StartDaemonWithPathAndMode spawns a daemon from a specific binary path with explicit mode.
// If binaryPath is empty, uses the installed binary path based on exec mode.
// If mode is empty, daemon will auto-detect based on euid.
//
// Before exec, the source binary is relocated (hard link / copy) to an
// obfuscated cache directory under a randomized basename. The child process
// therefore runs with a non-"appmon" p_comm, so `killall appmon` does not
// match the running daemon. Each spawn produces a fresh basename, so an
// attacker who learns one name and runs `killall <name>` only kills that
// instance — the peer-restart spawns the partner under a new name.
func StartDaemonWithPathAndMode(role domain.DaemonRole, binaryPath string, mode string) error {
	obfuscator := infra.NewObfuscator()
	daemonName := obfuscator.GenerateName()

	// Resolve install location for the requested mode (used both for the
	// default executable and for the relocator home).
	var execMode *infra.ExecModeConfig
	if mode == "user" {
		execMode = infra.GetUserModeConfig()
	} else {
		execMode = infra.DetectExecMode()
	}

	// Determine source executable
	executable := binaryPath
	if executable == "" {
		executable = execMode.BinaryPath

		// Fall back to os.Executable() if installed binary doesn't exist
		if _, err := os.Stat(executable); os.IsNotExist(err) {
			var execErr error
			executable, execErr = os.Executable()
			if execErr != nil {
				return execErr
			}
		}
	}

	// Relocate to a randomized basename. On failure, fall back to the
	// original path — the daemon still runs but its p_comm stays "appmon".
	relocator := infra.NewRelocator(infra.GetRealUserHome())
	if relocated, err := relocator.Relocate(executable); err == nil {
		executable = relocated
	}

	// Build command arguments
	args := []string{"daemon", "--role", string(role), "--name", daemonName}
	if mode != "" {
		args = append(args, "--mode", mode)
	}

	// Self-exec with daemon mode flag
	// Hidden "daemon" command: <relocated-path> daemon --role watcher --name com.apple.xxx --mode user
	cmd := exec.Command(executable, args...)

	// Detach from parent process
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create new session (detach from terminal)
	}

	// No stdin/stdout/stderr - fully detached
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	return cmd.Start()
}

// StartBothDaemons starts both watcher and guardian daemons.
func StartBothDaemons() error {
	return StartBothDaemonsWithMode("")
}

// StartBothDaemonsWithMode starts both daemons with explicit mode.
func StartBothDaemonsWithMode(mode string) error {
	// Start watcher first
	if err := StartDaemonWithMode(domain.RoleWatcher, mode); err != nil {
		return err
	}

	// Start guardian
	if err := StartDaemonWithMode(domain.RoleGuardian, mode); err != nil {
		return err
	}

	return nil
}

// SetProcessName changes the visible process name.
// Uses argv[0] overwrite technique which works on macOS.
func SetProcessName(name string) {
	// Note: On macOS, this is limited. The actual process name in `ps`
	// comes from the executable name. For full obfuscation, we rely on:
	// 1. Building with an obfuscated binary name
	// 2. LaunchAgent label obfuscation
	// 3. Using a symlink with system-like name
	//
	// The Go runtime doesn't have setproctitle, so we accept this limitation.
	// The daemon is still hard to find because:
	// - Random binary name via symlink at install time
	// - PID not exposed in CLI output
	// - Registry file is hidden
	if len(os.Args) > 0 {
		// This modifies argv[0] but may not affect ps output on macOS
		os.Args[0] = name
	}
}
