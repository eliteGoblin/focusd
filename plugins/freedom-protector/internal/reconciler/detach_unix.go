//go:build darwin || linux

package reconciler

import (
	"os/exec"
	"syscall"
)

// detach puts the child in its own session/process group so it is fully
// decoupled from this short-lived job (no shared controlling terminal,
// not killed when the job's process group goes away).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
