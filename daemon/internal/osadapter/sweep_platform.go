package osadapter

import (
	"os"
	"path/filepath"

	"github.com/eliteGoblin/focusd/daemon/internal/platdir"
)

// SweepStalePlatformWorkdirs removes disposable platform-workdirs (FEATURE 21 /
// HF1) left under supportRoot by prior generations — every directory carrying the
// platform-workdir CONTENT sentinel EXCEPT keepPlatformWorkdir (the one the
// current pointer references). Each removal is GATED by safeToRemoveWorkdir
// (absolute, strictly under supportRoot, not the keep, not an ancestor of it).
//
// FEATURE 26 (destructive-safety core): disguised directory names no longer carry
// a leading dot, so this sweep can no longer pre-filter by a "." prefix — it must
// scan ALL immediate children and decide by CONTENT. The ONLY thing that gates a
// RemoveAll is a positive platdir.IsPlatformWorkdir (a sentinel file whose bytes
// un-mask to the platform-workdir magic, or the legacy two-signal migration
// marker). A daemon-home carries a DIFFERENT magic (never matches here), and a
// real app folder (Google/, Spotify/) carries neither → is never a candidate. No
// name heuristic, no state.db heuristic, ever gates a delete.
//
// Cross-platform (pure filesystem, no launchd) so the install path compiles and
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
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(supportRoot, e.Name())
		if filepath.Clean(dir) == keep {
			continue // the live platform-workdir — never sweep it
		}
		// CONTENT gate: only a positive platform-workdir sentinel match. A
		// daemon-home (different magic) and every real app folder (no magic) are
		// skipped here — nothing else can reach the RemoveAll below.
		if !platdir.IsPlatformWorkdir(dir) {
			continue
		}
		if safeToRemoveWorkdir(dir, supportRoot, keepPlatformWorkdir) {
			if rmErr := os.RemoveAll(dir); rmErr == nil {
				removed++
			}
		}
	}
	return removed, nil
}
