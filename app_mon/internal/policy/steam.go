package policy

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/user/focusd/app_mon/internal/domain"
)

// SteamPolicy implements AppPolicy for blocking Steam.
type SteamPolicy struct {
	homeDir string
}

// NewSteamPolicy creates a new Steam blocking policy.
func NewSteamPolicy() *SteamPolicy {
	home, _ := os.UserHomeDir()
	return &SteamPolicy{homeDir: home}
}

// NewSteamPolicyWithHome creates a Steam policy with a custom home directory (for testing).
func NewSteamPolicyWithHome(homeDir string) *SteamPolicy {
	return &SteamPolicy{homeDir: homeDir}
}

func (p *SteamPolicy) ID() string {
	return "steam"
}

func (p *SteamPolicy) Name() string {
	return "Steam"
}

// ProcessPatterns returns Steam process names to kill.
// These are the known process names on macOS.
func (p *SteamPolicy) ProcessPatterns() []string {
	return []string{
		"Steam",
		"steam_osx",
		"steamwebhelper",
		"Steam Helper",
		"steamapps",
	}
}

// PathsToDelete returns all Steam-related paths to remove.
// Includes app bundle, user data, caches, and Homebrew installation.
func (p *SteamPolicy) PathsToDelete() []string {
	return []string{
		// Main application (DMG install)
		"/Applications/Steam.app",

		// User-space application
		filepath.Join(p.homeDir, "Applications/Steam.app"),

		// Steam user data and settings
		filepath.Join(p.homeDir, "Library/Application Support/Steam"),

		// Steam caches
		filepath.Join(p.homeDir, "Library/Caches/com.valvesoftware.steam"),

		// Steam preferences
		filepath.Join(p.homeDir, "Library/Preferences/com.valvesoftware.steam.plist"),

		// Steam saved state
		filepath.Join(p.homeDir, "Library/Saved Application State/com.valvesoftware.steam.savedState"),

		// Homebrew installation
		"/opt/homebrew/Caskroom/steam",

		// Intel Homebrew
		"/usr/local/Caskroom/steam",

		// Downloaded DMG files (glob pattern - handled specially)
		// Note: These are patterns, infrastructure layer expands them
		filepath.Join(p.homeDir, "Downloads/*[Ss]team*.dmg"),
		filepath.Join(p.homeDir, "Downloads/*[Ss]team*.zip"),
	}
}

func (p *SteamPolicy) ScanInterval() time.Duration {
	return DefaultScanInterval
}

func (p *SteamPolicy) PreEnforce(ctx context.Context) error {
	// No pre-enforcement hooks for Steam
	return nil
}

func (p *SteamPolicy) PostEnforce(ctx context.Context, result *domain.EnforcementResult) error {
	// No post-enforcement hooks for Steam
	return nil
}

// Ensure SteamPolicy implements AppPolicy.
var _ AppPolicy = (*SteamPolicy)(nil)
