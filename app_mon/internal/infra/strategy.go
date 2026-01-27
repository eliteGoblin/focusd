package infra

import (
	"bytes"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// BrewStrategy handles Homebrew cask uninstallation (macOS)
type BrewStrategy struct {
	brewPath string
}

// NewBrewStrategy creates a new Homebrew strategy
func NewBrewStrategy() *BrewStrategy {
	// Find brew binary
	brewPath, err := exec.LookPath("brew")
	if err != nil {
		// Try common locations
		for _, path := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"} {
			if _, err := exec.LookPath(path); err == nil {
				brewPath = path
				break
			}
		}
	}
	return &BrewStrategy{brewPath: brewPath}
}

func (b *BrewStrategy) Name() string {
	return "brew"
}

func (b *BrewStrategy) IsAvailable() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	return b.brewPath != ""
}

func (b *BrewStrategy) IsInstalled(pkg string) bool {
	if !b.IsAvailable() {
		return false
	}

	// Check if cask is installed: brew list --cask | grep -q pkg
	cmd := exec.Command(b.brewPath, "list", "--cask")
	cmd.Stdin = nil // Prevent any interactive prompts
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	// Check if package is in the list
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == pkg {
			return true
		}
	}
	return false
}

func (b *BrewStrategy) Uninstall(pkg string) error {
	if !b.IsAvailable() {
		return nil
	}

	// Skip brew uninstall if not running as root
	// brew may trigger GUI password dialog via osascript which we can't suppress
	// Path deletion will handle the actual removal instead
	if os.Getuid() != 0 {
		return nil
	}

	// brew uninstall --cask --force pkg
	cmd := exec.Command(b.brewPath, "uninstall", "--cask", "--force", pkg)
	cmd.Stdin = nil
	cmd.Stdout = nil
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// If package not installed, that's fine
		if strings.Contains(stderr.String(), "is not installed") {
			return nil
		}
		return nil
	}
	return nil
}

// StrategyManager discovers and manages available uninstall strategies
type StrategyManager struct {
	strategies []domain.UninstallStrategy
}

// NewStrategyManager creates a manager with all available strategies for this platform
func NewStrategyManager() *StrategyManager {
	sm := &StrategyManager{
		strategies: make([]domain.UninstallStrategy, 0),
	}

	// Add platform-specific strategies
	if runtime.GOOS == "darwin" {
		brew := NewBrewStrategy()
		if brew.IsAvailable() {
			sm.strategies = append(sm.strategies, brew)
		}
	}

	// Future: Add apt, snap, flatpak for Linux
	// Future: Add winget, chocolatey for Windows

	return sm
}

// GetStrategies returns all available strategies
func (sm *StrategyManager) GetStrategies() []domain.UninstallStrategy {
	return sm.strategies
}

// UninstallApp tries to uninstall an app using all available strategies
// Returns the strategy name that succeeded, or empty if none worked
func (sm *StrategyManager) UninstallApp(appName string) (string, error) {
	// Normalize app name (lowercase, common variations)
	pkgName := strings.ToLower(appName)

	for _, strategy := range sm.strategies {
		installed := strategy.IsInstalled(pkgName)
		if installed {
			if err := strategy.Uninstall(pkgName); err != nil {
				continue // Try next strategy
			}
			return strategy.Name(), nil
		}
	}
	return "", nil
}

// Ensure implementations satisfy interfaces
var _ domain.UninstallStrategy = (*BrewStrategy)(nil)
var _ domain.StrategyManager = (*StrategyManager)(nil)
