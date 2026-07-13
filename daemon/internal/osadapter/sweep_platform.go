package osadapter

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/eliteGoblin/focusd/daemon/internal/platdir"
)

// SweepStalePlatformWorkdirs removes disposable platform-workdirs (FEATURE 21 /
// HF1) left under supportRoot by prior generations — every hidden-dot directory
// carrying the platform-workdir sentinel EXCEPT keepPlatformWorkdir (the one the
// current pointer references). Each removal is GATED by safeToRemoveWorkdir
// (absolute, strictly under supportRoot, not the keep, not an ancestor of it).
//
// The sentinel is what tells a platform-workdir apart from a daemon-home, so a
// daemon-home (no sentinel) is NEVER a candidate here — the daemon-home orphan
// sweep (SweepOrphanWorkdirs) stays separate and skips sentinel dirs. This is
// cross-platform (pure filesystem, no launchd) so the install path compiles and
// behaves identically on darwin and the Linux/Windows build targets.
//
// Best-effort throughout: returns only a count (never the disguised paths); a
// failed remove is skipped, a scan failure returns the count so far with the
// error for optional logging.
func SweepStalePlatformWorkdirs(supportRoot, keepPlatformWorkdir string) (removed int, err error) {
	entries, rerr := os.ReadDir(supportRoot)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return 0, nil
		}
		return 0, rerr
	}
	keep := filepath.Clean(keepPlatformWorkdir)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), ".") {
			continue // platform-workdirs are hidden-dot dirs (relocate.HiddenWorkdir)
		}
		dir := filepath.Join(supportRoot, e.Name())
		if filepath.Clean(dir) == keep {
			continue // the live platform-workdir — never sweep it
		}
		if !platdir.IsPlatformWorkdir(dir) {
			continue // only sweep sentinel-marked platform-workdirs (never a daemon-home)
		}
		if safeToRemoveWorkdir(dir, supportRoot, keepPlatformWorkdir) {
			if rmErr := os.RemoveAll(dir); rmErr == nil {
				removed++
			}
		}
	}
	return removed, nil
}
