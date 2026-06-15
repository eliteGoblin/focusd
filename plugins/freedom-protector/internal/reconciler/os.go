package reconciler

import (
	"context"
	"os"
	"os/exec"
)

// launchDetached is the real launcher: it starts name+args fully detached
// from this short-lived job process so the relaunched app/proxy survives
// the job exiting. The launchTimeout is already enforced at the runLaunch
// ctx level; here we use a plain exec.Command (no CommandContext) so we do
// NOT spawn a per-launch watchdog goroutine that would leak when we Release
// the child without Wait. ctx is accepted for interface symmetry / future
// use but intentionally not wired to a kill — Release re-parents the child
// to launchd/init and we never Wait, so a CommandContext watcher would
// block forever on the unreaped process.
func launchDetached(_ context.Context, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	// Detach from the controlling process group and discard stdio so the
	// child keeps running after this job exits and never blocks on a pipe.
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the handle without waiting on the child: Release lets the OS
	// re-parent it to launchd/init. We intentionally do NOT Wait.
	return cmd.Process.Release()
}

// pathExists is the real stat seam.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
