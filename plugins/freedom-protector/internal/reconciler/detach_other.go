//go:build !darwin && !linux

package reconciler

import "os/exec"

// detach is a no-op on platforms without POSIX process groups. The plugin
// is darwin-only (see plugin.json supported_os); this stub exists solely
// so the cross-platform workspace build (windows) still compiles.
func detach(_ *exec.Cmd) {}
