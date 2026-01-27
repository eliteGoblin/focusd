// Package infra implements infrastructure concerns.
package infra

import (
	"os"
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
	IsRoot     bool   // Whether running as root
	// Note: BackupDir is intentionally not included here.
	// BackupManager uses its own obfuscated backup locations for security.
}

const (
	launchdLabel = "com.focusd.appmon"
)

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
			IsRoot:     true,
		}
	}

	return &ExecModeConfig{
		Mode:       ExecModeUser,
		BinaryPath: filepath.Join(home, ".local", "bin", "appmon"),
		PlistDir:   filepath.Join(home, "Library", "LaunchAgents"),
		PlistPath:  filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"),
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
