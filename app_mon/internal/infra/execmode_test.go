package infra

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectExecMode_ReturnsCorrectPaths(t *testing.T) {
	// This test verifies that DetectExecMode returns consistent paths
	// based on the current user's effective UID.
	// Note: We can't easily test root mode without actually being root,
	// but we can verify the user mode paths are correct.

	config := DetectExecMode()

	// In test environment, we're running as a regular user
	if os.Geteuid() == 0 {
		// Running as root (e.g., in CI with sudo)
		if config.Mode != ExecModeSystem {
			t.Errorf("expected system mode when euid=0, got %s", config.Mode)
		}
		if config.BinaryPath != "/usr/local/bin/appmon" {
			t.Errorf("expected /usr/local/bin/appmon, got %s", config.BinaryPath)
		}
		if config.PlistDir != "/Library/LaunchDaemons" {
			t.Errorf("expected /Library/LaunchDaemons, got %s", config.PlistDir)
		}
	} else {
		// Running as regular user
		if config.Mode != ExecModeUser {
			t.Errorf("expected user mode when euid!=0, got %s", config.Mode)
		}

		home, _ := os.UserHomeDir()
		expectedBinaryPath := filepath.Join(home, ".local", "bin", "appmon")
		if config.BinaryPath != expectedBinaryPath {
			t.Errorf("expected %s, got %s", expectedBinaryPath, config.BinaryPath)
		}

		expectedPlistDir := filepath.Join(home, "Library", "LaunchAgents")
		if config.PlistDir != expectedPlistDir {
			t.Errorf("expected %s, got %s", expectedPlistDir, config.PlistDir)
		}
	}
}

func TestExecModeConfig_PathsAreConsistent(t *testing.T) {
	// Verify that PlistPath is inside PlistDir
	config := DetectExecMode()

	plistDir := filepath.Dir(config.PlistPath)
	if plistDir != config.PlistDir {
		t.Errorf("PlistPath (%s) should be inside PlistDir (%s)", config.PlistPath, config.PlistDir)
	}

	// Verify binary path ends with "appmon"
	if filepath.Base(config.BinaryPath) != "appmon" {
		t.Errorf("BinaryPath should end with 'appmon', got %s", config.BinaryPath)
	}
}

func TestExecMode_String(t *testing.T) {
	tests := []struct {
		mode     ExecMode
		expected string
	}{
		{ExecModeUser, "user (LaunchAgent, non-root)"},
		{ExecModeSystem, "system (LaunchDaemon, root)"},
		{ExecMode("invalid"), "unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			if got := tt.mode.String(); got != tt.expected {
				t.Errorf("ExecMode.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestNewLaunchdManager_RespectsMode(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		name         string
		config       *ExecModeConfig
		expectedPath string
	}{
		{
			name: "user mode uses LaunchAgents",
			config: &ExecModeConfig{
				Mode:      ExecModeUser,
				PlistDir:  filepath.Join(home, "Library", "LaunchAgents"),
				PlistPath: filepath.Join(home, "Library", "LaunchAgents", "com.focusd.appmon.plist"),
			},
			expectedPath: filepath.Join(home, "Library", "LaunchAgents", "com.focusd.appmon.plist"),
		},
		{
			name: "system mode uses LaunchDaemons",
			config: &ExecModeConfig{
				Mode:      ExecModeSystem,
				PlistDir:  "/Library/LaunchDaemons",
				PlistPath: "/Library/LaunchDaemons/com.focusd.appmon.plist",
			},
			expectedPath: "/Library/LaunchDaemons/com.focusd.appmon.plist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewLaunchdManager(tt.config)
			impl := manager.(*LaunchdManagerImpl)

			if impl.plistPath != tt.expectedPath {
				t.Errorf("plistPath = %q, want %q", impl.plistPath, tt.expectedPath)
			}

			if impl.mode != tt.config.Mode {
				t.Errorf("mode = %q, want %q", impl.mode, tt.config.Mode)
			}
		})
	}
}

// TestDaemonModeDetection_BugRegression is a regression test for the bug where
// daemon subprocess always used user mode regardless of actual execution context.
// Bug: daemon subprocess used NewLaunchAgentManager() instead of DetectExecMode()
// Fix: daemon now calls DetectExecMode() to auto-detect based on euid
func TestDaemonModeDetection_BugRegression(t *testing.T) {
	// This test ensures that the mode detection logic works correctly
	// and that NewLaunchdManager respects the detected mode.

	// Simulate what happens in daemon subprocess
	execMode := DetectExecMode()
	manager := NewLaunchdManager(execMode)
	impl := manager.(*LaunchdManagerImpl)

	// The manager's mode should match the detected mode
	if impl.mode != execMode.Mode {
		t.Errorf("LaunchdManager mode (%s) doesn't match DetectExecMode (%s)",
			impl.mode, execMode.Mode)
	}

	// The plist path should match the detected config
	if impl.plistPath != execMode.PlistPath {
		t.Errorf("LaunchdManager plistPath (%s) doesn't match DetectExecMode (%s)",
			impl.plistPath, execMode.PlistPath)
	}

	// Verify the correct plist location based on mode
	if execMode.Mode == ExecModeUser {
		home, _ := os.UserHomeDir()
		if impl.plistDir != filepath.Join(home, "Library", "LaunchAgents") {
			t.Errorf("user mode should use ~/Library/LaunchAgents, got %s", impl.plistDir)
		}
	} else {
		if impl.plistDir != "/Library/LaunchDaemons" {
			t.Errorf("system mode should use /Library/LaunchDaemons, got %s", impl.plistDir)
		}
	}
}
