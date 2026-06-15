package reconciler

import (
	"context"
	"os"
	"os/exec"

	"github.com/shirou/gopsutil/v3/process"
)

// listProcesses is the real procLister: it reads the live process table.
// It prefers each process's absolute executable path (Exe) so matching is
// precise (FreedomProxy vs Freedom), falling back to the basename Name
// when Exe is unreadable (sandboxed/short-lived procs). A process that
// vanishes mid-scan is skipped, never fatal.
func listProcesses() ([]procView, error) {
	ps, err := process.Processes()
	if err != nil {
		return nil, err
	}
	out := make([]procView, 0, len(ps))
	for _, p := range ps {
		v := procView{PID: int(p.Pid)}
		if exe, err := p.Exe(); err == nil {
			v.Path = exe
		}
		if name, err := p.Name(); err == nil {
			v.Name = name
		}
		if v.Path == "" && v.Name == "" {
			continue // process vanished or unreadable; skip
		}
		out = append(out, v)
	}
	return out, nil
}

// launchDetached is the real launcher: it starts name+args fully detached
// from this short-lived job process so the relaunched app/proxy survives
// the job exiting, while still respecting ctx's deadline for the *start*
// itself (Start returns immediately; ctx guards against a wedged exec/IO).
func launchDetached(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
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
