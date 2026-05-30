//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
)

// configureProc puts the plugin in its own process group (so context-
// cancel kills the whole group) and, when plan.action == dropToUser,
// makes the child fork→setuid to the console user before exec with a
// reseeded environment.
//
// Without the process group a plugin that spawns children (e.g. a shell
// running `sleep`) would keep the stdout pipe open after the leader is
// killed, blocking cmd.Run past the timeout.
//
// The Credential is applied by the kernel between fork and exec, so this
// does NOT change the platform's own privileges — only the child's. The
// AMFI path-rotation (CDHash recomputed at exec from the on-disk inode)
// is unaffected by the credential.
func configureProc(cmd *exec.Cmd, plan dropPlan) {
	attr := &syscall.SysProcAttr{Setpgid: true}
	if plan.action == dropToUser {
		attr.Credential = &syscall.Credential{
			Uid: uint32(plan.uid),
			Gid: uint32(plan.gid),
			// Don't try to set supplementary groups: as root we could,
			// but we deliberately keep it minimal — the plugin only needs
			// the user's own files. NoSetGroups avoids an EPERM-prone
			// setgroups call and keeps the drop simple/predictable.
			NoSetGroups: true,
		}
		// Reseed the environment: root's HOME/TMPDIR would corrupt or
		// break the child (see privdrop.go). Replace it wholesale.
		cmd.Env = plan.dropEnv()
	}
	cmd.SysProcAttr = attr
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid => signal the entire process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
