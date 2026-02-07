// Package infra implements infrastructure concerns.
package infra

import (
	"os"
	"os/user"
	"path/filepath"
)

// ExecMode represents the execution mode of the application.
type ExecMode string

const (
	// ExecModeUser runs as user with LaunchAgent (no sudo required)
	ExecModeUser ExecMode = "user"
	// ExecModeSystem runs as root with LaunchDaemon (sudo required)
	ExecModeSystem ExecMode = "system"
)

// ExecModeConfig holds paths and settings based on execution mode.
type ExecModeConfig struct {
	Mode       ExecMode
	BinaryPath string // Where the binary should be installed
	PlistDir   string // Where the plist file goes
	PlistPath  string // Full path to plist file
	DataDir    string // Where encrypted registry and key live
	IsRoot     bool   // Whether running as root
	// Note: BackupDir is intentionally not included here.
	// BackupManager uses its own obfuscated backup locations for security.
}

const (
	// DefaultLaunchdLabel is the fallback label used before a randomized one is generated.
	DefaultLaunchdLabel = "com.focusd.appmon"
)

// launchdLabel is the active label. Defaults to the static name until overridden
// by a randomized label from the encrypted registry on startup.
var launchdLabel = DefaultLaunchdLabel

// SetLaunchdLabel overrides the plist label with a randomized one from the secret store.
func SetLaunchdLabel(label string) {
	launchdLabel = label
}

// GetLaunchdLabel returns the currently active plist label.
func GetLaunchdLabel() string {
	return launchdLabel
}

// DetectExecMode determines the execution mode based on effective UID.
func DetectExecMode() *ExecModeConfig {
	isRoot := os.Geteuid() == 0
	home, _ := os.UserHomeDir()

	if isRoot {
		return &ExecModeConfig{
			Mode:       ExecModeSystem,
			BinaryPath: "/usr/local/bin/appmon",
			PlistDir:   "/Library/LaunchDaemons",
			PlistPath:  "/Library/LaunchDaemons/" + launchdLabel + ".plist",
			DataDir:    "/var/lib/appmon",
			IsRoot:     true,
		}
	}

	return &ExecModeConfig{
		Mode:       ExecModeUser,
		BinaryPath: filepath.Join(home, ".local", "bin", "appmon"),
		PlistDir:   filepath.Join(home, "Library", "LaunchAgents"),
		PlistPath:  filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"),
		DataDir:    filepath.Join(home, ".appmon"),
		IsRoot:     false,
	}
}

// String returns a human-readable description of the mode.
func (m ExecMode) String() string {
	switch m {
	case ExecModeSystem:
		return "system (LaunchDaemon, root)"
	case ExecModeUser:
		return "user (LaunchAgent, non-root)"
	default:
		return "unknown"
	}
}

// GetUserModeConfig returns user mode config regardless of current euid.
// Used when running with sudo but wanting to install in user mode (--mode user).
// When running under sudo, uses SUDO_USER to get the invoking user's home directory.
func GetUserModeConfig() *ExecModeConfig {
	home := GetRealUserHome()
	return &ExecModeConfig{
		Mode:       ExecModeUser,
		BinaryPath: filepath.Join(home, ".local", "bin", "appmon"),
		PlistDir:   filepath.Join(home, "Library", "LaunchAgents"),
		PlistPath:  filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"),
		DataDir:    filepath.Join(home, ".appmon"),
		IsRoot:     os.Geteuid() == 0, // Still track actual root status for permission operations
	}
}

// GetRealUserHome returns the real user's home directory, even when running under sudo.
// Under sudo, os.UserHomeDir() returns /var/root, so we use SUDO_USER to find the real user.
func GetRealUserHome() string {
	// Check if running under sudo
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		if u, err := user.Lookup(sudoUser); err == nil {
			return u.HomeDir
		}
	}
	// Fall back to default
	home, _ := os.UserHomeDir()
	return home
}
