//go:build !darwin

package status

import "runtime"

// Gather on non-darwin platforms has no launchd mesh / disguised install to
// inspect. It returns an honest "unknown" snapshot and an unavailable
// platform detail so the command still renders (and exits 0) rather than
// pretending to a health read it cannot perform. `daemon status` itself is
// only meaningful on macOS, where the launchd mesh lives.
func Gather(workdirOverride string, jsonMode bool) (Snapshot, PlatformDetail) {
	return Snapshot{
		Mode:                runtime.GOOS,
		MeshUnknown:         true,
		VersionsUnknown:     true,
		GenerationsUnknown:  true,
		PlatformUnavailable: true,
	}, PlatformDetail{Available: false}
}
