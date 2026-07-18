//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup puts cmd in its OWN process group (Setpgid) so
// killProcessGroup can reap the whole subtree — including launchctl
// grandchildren — on timeout. See execWatchdogCtx (#106-b3): the watchdog
// rebuild shells out to launchctl, and a hang there would otherwise orphan
// the grandchild if only the direct child were killed.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup SIGKILLs the whole process group (the child + its
// launchctl grandchildren). Falls back to killing just the direct child if
// the group signal fails (e.g. the child already exited).
func killProcessGroup(cmd *exec.Cmd) error {
	if perr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); perr != nil {
		return cmd.Process.Kill()
	}
	return nil
}
