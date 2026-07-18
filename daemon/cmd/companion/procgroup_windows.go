//go:build windows

package main

import "os/exec"

// configureProcessGroup is a no-op on windows (#108). Process-group Setpgid
// semantics are unix-only, and the grandchild-reaping they guard against
// (a stuck launchctl) doesn't apply here — launchd/launchctl are darwin-only.
// The windows companion is future-ready, not functionally deployed yet, so it
// only needs to compile and provide a best-effort single-process kill.
func configureProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup kills just the direct child process (best-effort; there
// is no process-group handle to reap on windows).
func killProcessGroup(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}
