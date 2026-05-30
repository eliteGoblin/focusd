//go:build darwin

package runner

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// consoleUID returns the uid of the user logged in at the graphical
// console via `stat -f %u /dev/console`. No cgo — we shell out, as the
// daemon does elsewhere. A return of 0 means root owns /dev/console,
// i.e. no one is logged in at the screen (loginwindow / fast-user-switch);
// the caller maps that to a skip.
func consoleUID() (int, error) {
	out, err := exec.Command("stat", "-f", "%u", "/dev/console").Output()
	if err != nil {
		return 0, fmt.Errorf("stat /dev/console: %w", err)
	}
	s := strings.TrimSpace(string(out))
	uid, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("parse console uid %q: %w", s, err)
	}
	return uid, nil
}
