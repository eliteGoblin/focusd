//go:build !darwin

package reconciler

import "errors"

// listProcesses is unsupported off darwin. The plugin is darwin-only (see
// plugin.json supported_os: ["darwin"]); this stub exists solely so the
// cross-platform workspace build (linux/windows) still compiles. Mirrors
// the detach_other.go fallback pattern.
func listProcesses() ([]procView, error) {
	return nil, errors.New("freedom-protector: process enumeration unsupported on this OS (darwin-only)")
}
