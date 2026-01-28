// Package infra implements infrastructure concerns (process, filesystem, registry).
package infra

import (
	"os"
	"os/exec"
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

// FreedomProtectorImpl implements domain.FreedomProtector.
// It monitors and protects Freedom app, restarting it if killed
// and restoring Login Items if removed.
type FreedomProtectorImpl struct {
	pm     domain.ProcessManager
	logger *zap.Logger
}

// NewFreedomProtector creates a new Freedom protector.
func NewFreedomProtector(pm domain.ProcessManager, logger *zap.Logger) *FreedomProtectorImpl {
	return &FreedomProtectorImpl{
		pm:     pm,
		logger: logger,
	}
}

// IsInstalled checks if Freedom.app exists at /Applications/Freedom.app
func (f *FreedomProtectorImpl) IsInstalled() bool {
	_, err := os.Stat(FreedomAppPath)
	return err == nil
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
	cmd := exec.Command("ps", "-p", string(rune(pid)), "-o", "comm=")
	output, err := cmd.Output()
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

	// Fallback: use ps to check by executable path (works for root processes)
	cmd := exec.Command("pgrep", "-f", FreedomHelperProcessName)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(output))) > 0
}

// IsLoginItemPresent checks if Freedom is in Login Items
func (f *FreedomProtectorImpl) IsLoginItemPresent() bool {
	// Use AppleScript to check Login Items
	script := `tell application "System Events" to get the name of every login item`
	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.Output()
	if err != nil {
		f.logger.Debug("failed to check login items", zap.Error(err))
		return false
	}
	// Output is comma-separated list of login item names
	items := string(output)
	return strings.Contains(items, "Freedom")
}

// RestartApp launches Freedom.app using `open -a`
func (f *FreedomProtectorImpl) RestartApp() error {
	f.logger.Info("restarting Freedom app")
	cmd := exec.Command("open", "-a", FreedomAppPath)
	if err := cmd.Run(); err != nil {
		f.logger.Error("failed to restart Freedom", zap.Error(err))
		return err
	}
	f.logger.Info("Freedom app restarted successfully")
	return nil
}

// RestoreLoginItem adds Freedom back to Login Items using AppleScript
func (f *FreedomProtectorImpl) RestoreLoginItem() error {
	f.logger.Info("restoring Freedom to Login Items")
	script := `tell application "System Events" to make login item at end with properties {path:"/Applications/Freedom.app", hidden:false}`
	cmd := exec.Command("osascript", "-e", script)
	if err := cmd.Run(); err != nil {
		f.logger.Error("failed to restore login item", zap.Error(err))
		return err
	}
	f.logger.Info("Freedom login item restored successfully")
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
		f.logger.Debug("Freedom not installed, skipping protection")
		return false, nil
	}

	// Check and restart app if killed
	if !f.IsAppRunning() {
		f.logger.Info("Freedom app not running, restarting...")
		if err := f.RestartApp(); err != nil {
			return false, err
		}
		actionTaken = true
	}

	// Check and restore login item if removed
	if !f.IsLoginItemPresent() {
		f.logger.Info("Freedom login item missing, restoring...")
		if err := f.RestoreLoginItem(); err != nil {
			return actionTaken, err
		}
		actionTaken = true
	}

	// Log helper status (we can't fix it, but we can report)
	if !f.IsHelperRunning() {
		f.logger.Warn("FreedomHelper not running (reinstall Freedom to fix)")
	}

	return actionTaken, nil
}

// Ensure FreedomProtectorImpl implements domain.FreedomProtector.
var _ domain.FreedomProtector = (*FreedomProtectorImpl)(nil)
