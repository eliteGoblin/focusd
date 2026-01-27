// Package infra implements infrastructure concerns (process, filesystem, registry).
package infra

import (
	"os"
	"strings"
	"syscall"

	"github.com/shirou/gopsutil/v3/process"

	"github.com/user/focusd/app_mon/internal/domain"
)

// ProcessManagerImpl implements domain.ProcessManager using gopsutil.
type ProcessManagerImpl struct{}

// NewProcessManager creates a new process manager.
func NewProcessManager() domain.ProcessManager {
	return &ProcessManagerImpl{}
}

// FindByName returns PIDs of processes matching the pattern (case-insensitive).
func (pm *ProcessManagerImpl) FindByName(pattern string) ([]int, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, err
	}

	var found []int
	patternLower := strings.ToLower(pattern)

	for _, p := range procs {
		name, err := p.Name()
		if err != nil {
			continue // Process may have exited
		}

		// Case-insensitive match
		if strings.EqualFold(name, pattern) || strings.Contains(strings.ToLower(name), patternLower) {
			found = append(found, int(p.Pid))
		}
	}

	return found, nil
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
