//go:build !darwin

package runner

import "errors"

// consoleUID is darwin-only (runtime privilege-drop targets macOS's
// /dev/console + launchd model). On other platforms the system-mode
// privilege-drop path is never reached in production, but we keep a
// stub so the package builds everywhere.
func consoleUID() (int, error) {
	return 0, errors.New("console-user discovery is darwin-only")
}
