//go:build windows

package osadapter

import (
	"fmt"
	"os"
	"path/filepath"
)

// NewAdapter returns the Windows adapter.
//
// Layout (per spec §Suggested app runtime folder layout):
//
//	user:   %LOCALAPPDATA%\focusd
//	system: %PROGRAMDATA%\focusd
//
// Windows lifecycle (SCM service) is a later phase; only paths and
// identity are wired now.
func NewAdapter() Adapter {
	return &baseAdapter{
		name: "windows",
		baseDirFor: func(mode RunMode) (string, error) {
			switch mode {
			case ModeSystem:
				root := os.Getenv("PROGRAMDATA")
				if root == "" {
					root = `C:\ProgramData`
				}
				return filepath.Join(root, AppName), nil
			case ModeUser:
				root := os.Getenv("LOCALAPPDATA")
				if root == "" {
					home, err := os.UserHomeDir()
					if err != nil {
						return "", fmt.Errorf("resolve home dir: %w", err)
					}
					root = filepath.Join(home, "AppData", "Local")
				}
				return filepath.Join(root, AppName), nil
			default:
				return "", fmt.Errorf("osadapter: invalid run mode %q", mode)
			}
		},
	}
}
