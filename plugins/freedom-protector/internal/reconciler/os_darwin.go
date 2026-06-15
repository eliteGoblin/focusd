//go:build darwin

package reconciler

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// listProcesses reads the live process table on macOS with a single
// `sysctl(kern.proc.all)` call via x/sys/unix. This is CGO-free and fast
// (one syscall for the whole table), unlike gopsutil's per-process Exe()
// which under CGO_ENABLED=0 falls back to spawning `lsof -p` PER PROCESS
// (~26s for ~850 procs) and blows past the 20s platform job timeout.
//
// We only have the kernel's truncated command name (P_comm, a 16-char
// NUL-terminated field), not the absolute executable path — so procView.Path
// is left empty and matching relies on the name/basename fallback in
// matchesAny. Both "Freedom" (7) and "FreedomProxy" (12) fit inside the
// 15-significant-char P_comm limit, so the exact Freedom-vs-FreedomProxy
// distinction is preserved.
func listProcesses() ([]procView, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("sysctl kern.proc.all: %w", err)
	}
	out := make([]procView, 0, len(procs))
	for i := range procs {
		kp := &procs[i]
		pid := int(kp.Proc.P_pid)
		if pid <= 0 {
			continue
		}
		name := commName(kp.Proc.P_comm[:])
		if name == "" {
			continue // unreadable/short-lived; skip, never fatal
		}
		out = append(out, procView{PID: pid, Name: name})
	}
	return out, nil
}

// commName decodes a NUL-terminated P_comm byte field into a Go string.
func commName(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
