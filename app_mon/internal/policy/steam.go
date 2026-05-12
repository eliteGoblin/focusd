package policy

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
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

// ProcessPatterns returns Steam process basenames to kill.
//
// Each entry is matched against gopsutil's Process.Name() (kernel
// p_comm or argv[0] basename) case-insensitively but EXACTLY — see
// FindByName for why substring matching is forbidden (it was killing
// Microsoft Teams via "MSTeams" substring of "steam").
//
// To cover all of Steam's runtime children on macOS we list every
// known basename rather than relying on substring fallback:
//   - Steam, steam_osx: launcher / 64-bit binary variants
//   - steamwebhelper, steamservice: helper daemons
//   - Steam Helper (+ Chromium subprocess variants): Steam's embedded
//     Chromium UI spawns separate processes per renderer/GPU/plugin
//
// If a new Steam variant ships we'll miss it on first run; the cost is
// one Steam subprocess surviving for ~10 seconds while we deploy a
// patch. That's strictly safer than re-introducing substring matching.
//
// "steamapps" was previously listed here pre-v0.6.1 but is a directory
// name, not a process. Harmless under exact match (no process is named
// that), removed for clarity.
func (p *SteamPolicy) ProcessPatterns() []string {
	return []string{
		"Steam",
		"steam_osx",
		"steamwebhelper",
		"steamservice",
		"Steam Helper",
		"Steam Helper (GPU)",
		"Steam Helper (Renderer)",
		"Steam Helper (Plugin)",
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
