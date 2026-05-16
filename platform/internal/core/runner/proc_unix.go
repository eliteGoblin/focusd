//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
)

// configureProc puts the plugin in its own process group and makes
// context-cancel kill the whole group. Without this a plugin that
// spawns children (e.g. a shell running `sleep`) would keep the stdout
// pipe open after the leader is killed, blocking cmd.Run past the
// timeout.
func configureProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid => signal the entire process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
