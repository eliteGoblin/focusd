//go:build darwin

package osadapter

import (
	"fmt"
	"os"
	"path/filepath"
)

// NewAdapter returns the macOS adapter.
//
// Layout (per spec §Suggested app runtime folder layout):
//
//	user:   ~/Library/Application Support/focusd
//	system: /Library/Application Support/focusd
//
// User and system roots are deliberately separate so the two modes are
// completely isolated.
func NewAdapter() Adapter {
	return &baseAdapter{
		name: "darwin",
		baseDirFor: func(mode RunMode) (string, error) {
			switch mode {
			case ModeSystem:
				return filepath.Join("/Library/Application Support", AppName), nil
			case ModeUser:
				home, err := os.UserHomeDir()
				if err != nil {
					return "", fmt.Errorf("resolve home dir: %w", err)
				}
				return filepath.Join(home, "Library/Application Support", AppName), nil
			default:
				return "", fmt.Errorf("osadapter: invalid run mode %q", mode)
			}
		},
	}
}
