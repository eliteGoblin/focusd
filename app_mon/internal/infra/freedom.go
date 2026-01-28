// Package infra implements infrastructure concerns (process, filesystem, registry).
package infra

import (
	"os"
	"os/exec"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// Freedom app constants
const (
	FreedomAppPath           = "/Applications/Freedom.app"
	FreedomProcessName       = "Freedom"
	FreedomProxyProcessName  = "FreedomProxy"
	FreedomHelperProcessName = "com.80pct.FreedomHelper"
	FreedomProxyPort         = 7769
)

// CommandRunner abstracts command execution for testing
type CommandRunner interface {
	Run(name string, args ...string) error
	Output(name string, args ...string) ([]byte, error)
}

// RealCommandRunner executes real system commands
type RealCommandRunner struct{}

// Run executes a command and waits for it to complete
func (r *RealCommandRunner) Run(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// Output executes a command and returns its stdout
func (r *RealCommandRunner) Output(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// FileChecker abstracts file system checks for testing
type FileChecker interface {
	Exists(path string) bool
}

// RealFileChecker checks real filesystem
type RealFileChecker struct{}

// Exists checks if a file/directory exists
func (r *RealFileChecker) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// FreedomProtectorImpl implements domain.FreedomProtector.
// It monitors and protects Freedom app, restarting it if killed
// and restoring Login Items if removed.
type FreedomProtectorImpl struct {
	pm          domain.ProcessManager
	logger      *zap.Logger
	cmdRunner   CommandRunner
	fileChecker FileChecker
}

// NewFreedomProtector creates a new Freedom protector.
func NewFreedomProtector(pm domain.ProcessManager, logger *zap.Logger) *FreedomProtectorImpl {
	return &FreedomProtectorImpl{
		pm:          pm,
		logger:      logger,
		cmdRunner:   &RealCommandRunner{},
		fileChecker: &RealFileChecker{},
	}
}

// NewFreedomProtectorWithDeps creates a protector with injectable dependencies (for testing)
func NewFreedomProtectorWithDeps(pm domain.ProcessManager, logger *zap.Logger, cmdRunner CommandRunner, fileChecker FileChecker) *FreedomProtectorImpl {
	return &FreedomProtectorImpl{
		pm:          pm,
		logger:      logger,
		cmdRunner:   cmdRunner,
		fileChecker: fileChecker,
	}
}

// IsInstalled checks if Freedom.app exists at /Applications/Freedom.app
func (f *FreedomProtectorImpl) IsInstalled() bool {
	return f.fileChecker.Exists(FreedomAppPath)
}

// IsAppRunning checks if Freedom main process is running
func (f *FreedomProtectorImpl) IsAppRunning() bool {
	pids, err := f.pm.FindByName(FreedomProcessName)
	if err != nil {
		return false
	}
	// Filter out FreedomProxy and FreedomHelper from results
	for _, pid := range pids {
		// FindByName does substring match, so we need to verify exact match
		// by checking that it's not the proxy or helper
		if f.isExactProcessMatch(pid, FreedomProcessName) {
			return true
		}
	}
	return false
}

// isExactProcessMatch verifies a PID matches a specific process name
// This is needed because FindByName does substring matching
func (f *FreedomProtectorImpl) isExactProcessMatch(pid int, expectedName string) bool {
	// Use ps to get exact process name
	output, err := f.cmdRunner.Output("ps", "-p", strconv.Itoa(pid), "-o", "comm=")
	if err != nil {
		// Process might have exited, check via gopsutil
		pids, _ := f.pm.FindByName(expectedName)
		for _, p := range pids {
			if p == pid {
				return true
			}
		}
		return false
	}
	name := strings.TrimSpace(string(output))
	return strings.HasSuffix(name, expectedName) && !strings.Contains(name, "Proxy") && !strings.Contains(name, "Helper")
}

// IsProxyRunning checks if FreedomProxy process is running
func (f *FreedomProtectorImpl) IsProxyRunning() bool {
	pids, err := f.pm.FindByName(FreedomProxyProcessName)
	if err != nil {
		return false
	}
	return len(pids) > 0
}

// IsHelperRunning checks if com.80pct.FreedomHelper is running.
// The helper runs as root, so gopsutil can't read its Name() directly.
// We use ps command to check by executable path instead.
func (f *FreedomProtectorImpl) IsHelperRunning() bool {
	// First try FindByName (works if process name is readable)
	pids, err := f.pm.FindByName(FreedomHelperProcessName)
	if err == nil && len(pids) > 0 {
		return true
	}

	// Fallback: use pgrep to check by process name (works for root processes)
	output, err := f.cmdRunner.Output("pgrep", "-f", FreedomHelperProcessName)
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(output))) > 0
}

// IsLoginItemPresent checks if Freedom is in Login Items
func (f *FreedomProtectorImpl) IsLoginItemPresent() bool {
	// Use AppleScript to check Login Items
	script := `tell application "System Events" to get the name of every login item`
	output, err := f.cmdRunner.Output("osascript", "-e", script)
	if err != nil {
		f.logDebug("failed to check login items", zap.Error(err))
		return false
	}
	// Output is comma-separated list of login item names
	items := string(output)
	return strings.Contains(items, "Freedom")
}

// RestartApp launches Freedom.app using `open -a`
func (f *FreedomProtectorImpl) RestartApp() error {
	f.logInfo("restarting Freedom app")
	if err := f.cmdRunner.Run("open", "-a", FreedomAppPath); err != nil {
		f.logError("failed to restart Freedom", zap.Error(err))
		return err
	}
	f.logInfo("Freedom app restarted successfully")
	return nil
}

// RestoreLoginItem adds Freedom back to Login Items using AppleScript
func (f *FreedomProtectorImpl) RestoreLoginItem() error {
	f.logInfo("restoring Freedom to Login Items")
	script := `tell application "System Events" to make login item at end with properties {path:"/Applications/Freedom.app", hidden:false}`
	if err := f.cmdRunner.Run("osascript", "-e", script); err != nil {
		f.logError("failed to restore login item", zap.Error(err))
		return err
	}
	f.logInfo("Freedom login item restored successfully")
	return nil
}

// GetHealth returns comprehensive health status
func (f *FreedomProtectorImpl) GetHealth() domain.FreedomHealth {
	return domain.FreedomHealth{
		Installed:        f.IsInstalled(),
		AppRunning:       f.IsAppRunning(),
		ProxyRunning:     f.IsProxyRunning(),
		HelperRunning:    f.IsHelperRunning(),
		LoginItemPresent: f.IsLoginItemPresent(),
		ProxyPort:        FreedomProxyPort,
	}
}

// Protect runs a protection cycle: restart app if needed, restore login item if needed
// Returns true if any action was taken
func (f *FreedomProtectorImpl) Protect() (actionTaken bool, err error) {
	// Skip if Freedom not installed
	if !f.IsInstalled() {
		f.logDebug("Freedom not installed, skipping protection")
		return false, nil
	}

	// Check and restart app if killed
	if !f.IsAppRunning() {
		f.logInfo("Freedom app not running, restarting...")
		if err := f.RestartApp(); err != nil {
			return false, err
		}
		actionTaken = true
	}

	// Check and restore login item if removed
	if !f.IsLoginItemPresent() {
		f.logInfo("Freedom login item missing, restoring...")
		if err := f.RestoreLoginItem(); err != nil {
			return actionTaken, err
		}
		actionTaken = true
	}

	// Log helper status (we can't fix it, but we can report)
	if !f.IsHelperRunning() {
		f.logWarn("FreedomHelper not running (reinstall Freedom to fix)")
	}

	return actionTaken, nil
}

// Logging helpers that handle nil logger
func (f *FreedomProtectorImpl) logDebug(msg string, fields ...zap.Field) {
	if f.logger != nil {
		f.logger.Debug(msg, fields...)
	}
}

func (f *FreedomProtectorImpl) logInfo(msg string, fields ...zap.Field) {
	if f.logger != nil {
		f.logger.Info(msg, fields...)
	}
}

func (f *FreedomProtectorImpl) logWarn(msg string, fields ...zap.Field) {
	if f.logger != nil {
		f.logger.Warn(msg, fields...)
	}
}

func (f *FreedomProtectorImpl) logError(msg string, fields ...zap.Field) {
	if f.logger != nil {
		f.logger.Error(msg, fields...)
	}
}

// Ensure FreedomProtectorImpl implements domain.FreedomProtector.
var _ domain.FreedomProtector = (*FreedomProtectorImpl)(nil)
