//go:build linux

package osadapter

import (
	"fmt"
	"os"
	"path/filepath"
)

// NewAdapter returns the Linux adapter.
//
// macOS is the primary target; Linux support exists mainly so the
// platform builds and its tests run in Linux CI. Layout follows XDG:
//
//	user:   $XDG_DATA_HOME/focusd  (default ~/.local/share/focusd)
//	system: /var/lib/focusd
func NewAdapter() Adapter {
	return &baseAdapter{
		name: "linux",
		baseDirFor: func(mode RunMode) (string, error) {
			switch mode {
			case ModeSystem:
				return filepath.Join("/var/lib", AppName), nil
			case ModeUser:
				if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
					return filepath.Join(xdg, AppName), nil
				}
				home, err := os.UserHomeDir()
				if err != nil {
					return "", fmt.Errorf("resolve home dir: %w", err)
				}
				return filepath.Join(home, ".local", "share", AppName), nil
			default:
				return "", fmt.Errorf("osadapter: invalid run mode %q", mode)
			}
		},
	}
}
