package daemon

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/user/focusd/app_mon/internal/domain"
	"github.com/user/focusd/app_mon/internal/infra"
)

// StartDaemon spawns a new daemon process with an obfuscated name.
// The daemon is detached from the parent process (runs independently).
func StartDaemon(role domain.DaemonRole) error {
	obfuscator := infra.NewObfuscator()
	daemonName := obfuscator.GenerateName()

	// Get our own executable path
	executable, err := os.Executable()
	if err != nil {
		return err
	}

	// Self-exec with daemon mode flag
	// Hidden "daemon" command: appmon daemon --role watcher --name com.apple.xxx
	cmd := exec.Command(executable, "daemon",
		"--role", string(role),
		"--name", daemonName)

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
	// Start watcher first
	if err := StartDaemon(domain.RoleWatcher); err != nil {
		return err
	}

	// Start guardian
	if err := StartDaemon(domain.RoleGuardian); err != nil {
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
