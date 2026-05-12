// Package infra implements infrastructure concerns (process, filesystem, registry).
package infra

import (
	"os"
	"strings"
	"syscall"

	"github.com/shirou/gopsutil/v3/process"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// ProcessManagerImpl implements domain.ProcessManager using gopsutil.
type ProcessManagerImpl struct{}

// NewProcessManager creates a new process manager.
func NewProcessManager() domain.ProcessManager {
	return &ProcessManagerImpl{}
}

// FindByName returns PIDs of processes whose basename matches the pattern
// case-insensitively but EXACTLY — not substring.
//
// Substring matching (the design before v0.6.1) was actively dangerous:
// pattern "Steam" substring-matched against Microsoft Teams' main binary
// "MSTeams" (lowercased "msteams" contains "steam"), killing Teams every
// quick-kill tick — interrupting work calls and meetings. Exact match
// closes that hole.
//
// The cost is that we must enumerate every basename of a blocked app's
// process names explicitly in the policy ("Steam", "steam_osx",
// "steamwebhelper", "Steam Helper", "Steam Helper (GPU)", etc.). Missing
// a variant means one Steam subprocess survives — safe and a tweak
// away. Matching too loosely could kill the user's email client — not.
//
// Names come from gopsutil's Process.Name(): on macOS it returns the
// kernel's p_comm (truncated to ~15 chars), falling back to argv[0]
// basename for longer process names.
func (pm *ProcessManagerImpl) FindByName(pattern string) ([]int, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, err
	}

	var found []int
	for _, p := range procs {
		name, err := p.Name()
		if err != nil {
			continue // Process may have exited
		}
		if processNameMatches(name, pattern) {
			found = append(found, int(p.Pid))
		}
	}
	return found, nil
}

// processNameMatches is the matching predicate used by FindByName,
// extracted so the v0.6.0-killing-Teams regression can be tested without
// running real processes. Returns true iff name == pattern under Unicode
// case folding — strict equality of basenames, no substring or prefix.
func processNameMatches(name, pattern string) bool {
	return strings.EqualFold(name, pattern)
}

// Kill terminates a process by PID using SIGKILL.
func (pm *ProcessManagerImpl) Kill(pid int) error {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return err
	}
	return p.Kill()
}

// IsRunning checks if a PID exists and is running.
func (pm *ProcessManagerImpl) IsRunning(pid int) bool {
	// On Unix, FindProcess always succeeds
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// Send signal 0 to check if process exists
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// GetCurrentPID returns the current process PID.
func (pm *ProcessManagerImpl) GetCurrentPID() int {
	return os.Getpid()
}

// Ensure ProcessManagerImpl implements domain.ProcessManager.
var _ domain.ProcessManager = (*ProcessManagerImpl)(nil)
