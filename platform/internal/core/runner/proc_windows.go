//go:build windows

package runner

import "os/exec"

// configureProc is a no-op on Windows; CommandContext's default kill of
// the process plus WaitDelay is sufficient for the current job model.
func configureProc(cmd *exec.Cmd) {}
