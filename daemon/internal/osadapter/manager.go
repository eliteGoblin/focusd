package osadapter

import (
	"fmt"
	"time"
)

// controller is the launchd seam (real on darwin, fake in tests).
type controller interface {
	loaded(label string) bool
	bootstrap(plistPath string) error
	bootout(label string) error
}

// fsio is the filesystem seam for plist files.
type fsio interface {
	write(path, content string) error
	remove(path string) error
	plistPath(label string) string
}

// rosterIO is the masked-roster-file seam (FEATURE 10 / ADR-0014). Real
// on darwin (core.WriteRoster/ReadRoster over the workdir .roster file);
// tests inject a fake. Kept OS-agnostic here so the install/ensure/
// uninstall ordering is unit-tested without launchd or a real FS.
type rosterIO interface {
	writeRoster(labels []string) error
	readRoster() ([]string, error)
	removeRoster() error
}

// rosterLabels returns the three mesh labels in AllRoles order — the
// payload persisted to the masked roster file.
func rosterLabels(s Spec) []string {
	labels := make([]string, len(AllRoles))
	for i, r := range AllRoles {
		labels[i] = s.Label(r)
	}
	return labels
}

// reloadAttempts is how many bootout+bootstrap tries robustReload makes before
// giving up. On a LIVE mesh, launchctl bootout returns BEFORE launchd finishes
// its async teardown, so an immediate bootstrap of a still-loaded label returns
// EIO ("Bootstrap failed: 5" / "already loaded"). Re-issuing bootout (idempotent)
// + a brief backoff absorbs that window; ~3 tries is ample.
const reloadAttempts = 3

// reloadBackoff is the delay before retry #attempt (attempt≥1): ~250ms, then
// ~500ms. Enough for launchd's async teardown to settle without a slow reconcile.
func reloadBackoff(attempt int) time.Duration {
	return time.Duration(attempt) * 250 * time.Millisecond
}

// robustReload boots out label then bootstraps pp, retrying bootout+bootstrap up
// to reloadAttempts times with a brief backoff between tries (issue #102-a). It
// exists because on a LIVE mesh a single bootout+bootstrap is racy:
//   - booting out the EXECUTING worker's OWN label SIGTERMs itself, and an
//     immediate bootstrap of that label returns EIO; and
//   - bootout of a SIBLING returns before launchd finishes async teardown, so an
//     immediate bootstrap of that sibling also returns EIO.
//
// bootout is idempotent, so re-issuing it before each retry clears the async
// "already-loaded" state. On a fresh install (labels not loaded) the first
// bootstrap succeeds and there is zero retry cost. sleep is injected so the retry
// path is unit-testable without real time (production passes time.Sleep).
func robustReload(c controller, label, pp string, sleep func(time.Duration)) error {
	var err error
	for attempt := 0; attempt < reloadAttempts; attempt++ {
		if attempt > 0 {
			sleep(reloadBackoff(attempt))
		}
		_ = c.bootout(label) // idempotent; clears any async-teardown "already loaded"
		if err = c.bootstrap(pp); err == nil {
			return nil
		}
	}
	return err
}

// installAll writes the masked roster FIRST (so a survivor relaunched
// before the plists settle can still recover), then writes + (re)loads
// all three mesh entries (idempotent: any stale instance is booted out
// first).
func installAll(s Spec, c controller, fs fsio, rs rosterIO) error {
	if rs != nil {
		if err := rs.writeRoster(rosterLabels(s)); err != nil {
			return fmt.Errorf("osadapter: write roster: %w", err)
		}
	}
	for _, r := range AllRoles {
		label := s.Label(r)
		pp := fs.plistPath(label)
		if err := fs.write(pp, Plist(s, r)); err != nil {
			return fmt.Errorf("osadapter: write %s: %w", label, err)
		}
		if err := robustReload(c, label, pp, time.Sleep); err != nil {
			return fmt.Errorf("osadapter: bootstrap %s: %w", label, err)
		}
	}
	return nil
}

// reinstallExceptSelf re-materializes the whole mesh at the new binary path
// WITHOUT any process ever bootstrapping its OWN label (issue #102). It writes the
// roster + all three plists, robustly reloads every label EXCEPT selfLabel, and
// then boots selfLabel OUT (last, no bootstrap).
//
// This replaces the old installAll on the in-mesh re-materialize path (102-a/b),
// where a surviving worker re-installing the mesh would bootout its OWN executing
// label mid-loop → SIGTERM itself → the platform-lock released → a sibling
// re-entered, saw the binary still "missing", and placed a SECOND binary (two
// binaries, mesh 2/3). Here the lock HOLDER completes the whole re-materialize
// while holding the lock (true single-actor), and never restarts its own label.
//
// Booting self out LAST leaves self genuinely !loaded, so the EXISTING ensureAll
// `!loaded` path — running on a freshly-reloaded sibling/ensurer — re-bootstraps
// self onto the new binary within one ~2s reconcile. No process bootstraps its own
// label. selfLabel must be a real role label; an empty selfLabel skips nothing
// (defensive: the mesh still comes back, at the old self-kill risk — but the sole
// caller is the lock-holding worker, whose role label is always known).
//
// CRITICAL failure semantics: if a NON-self label's reload permanently fails we
// return the error BEFORE booting self out, so self stays loaded (never left
// down). The binary was already placed by the caller (ensureBinaryPresent), which
// reports changed=true regardless — the caller adopts the new path so the next
// tick's ensureAll retries the launchd side against the CORRECT path.
//
// sleep is injected (mirrors robustReload) so the partial-failure path is
// unit-tested without real backoff time; production passes time.Sleep.
func reinstallExceptSelf(s Spec, c controller, fs fsio, rs rosterIO, selfLabel string, sleep func(time.Duration)) error {
	if rs != nil {
		if err := rs.writeRoster(rosterLabels(s)); err != nil {
			return fmt.Errorf("osadapter: write roster: %w", err)
		}
	}
	// Write all three plists at the new path FIRST so the roster + plists agree
	// before any (re)load.
	for _, r := range AllRoles {
		label := s.Label(r)
		if err := fs.write(fs.plistPath(label), Plist(s, r)); err != nil {
			return fmt.Errorf("osadapter: write %s: %w", label, err)
		}
	}
	// Reload every label EXCEPT self.
	for _, r := range AllRoles {
		label := s.Label(r)
		if label == selfLabel {
			continue
		}
		if err := robustReload(c, label, fs.plistPath(label), sleep); err != nil {
			return fmt.Errorf("osadapter: bootstrap %s: %w", label, err)
		}
	}
	// Boot self OUT last (no bootstrap): self ends genuinely !loaded, and the
	// freshly-reloaded sibling/ensurer's next ensureAll re-bootstraps it.
	if selfLabel != "" {
		_ = c.bootout(selfLabel)
	}
	return nil
}

// uninstallAll boots out and removes all three entries (idempotent), then
// removes the masked roster file LAST — after the mesh it described is
// gone, so a mid-uninstall survivor can still recover until the end.
func uninstallAll(s Spec, c controller, fs fsio, rs rosterIO) error {
	var first error
	for _, r := range AllRoles {
		label := s.Label(r)
		_ = c.bootout(label)
		if err := fs.remove(fs.plistPath(label)); err != nil && first == nil {
			first = err
		}
	}
	if rs != nil {
		if err := rs.removeRoster(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ensureAll recreates any missing entry — the mutual self-healing the
// mesh relies on (a survivor rebuilds dead siblings) — and self-heals the
// masked roster file from the in-memory roster if it is missing or
// corrupt (acceptance #4). Idempotent.
func ensureAll(s Spec, c controller, fs fsio, rs rosterIO) (recreated []Role, err error) {
	if rs != nil {
		if _, rerr := rs.readRoster(); rerr != nil {
			// Missing/tampered/corrupt → rewrite from the in-memory roster.
			// Best-effort: a roster-write failure must not block plist
			// self-heal (the in-memory roster keeps the mesh running).
			_ = rs.writeRoster(rosterLabels(s))
		}
	}
	for _, r := range AllRoles {
		label := s.Label(r)
		if c.loaded(label) {
			continue
		}
		pp := fs.plistPath(label)
		if werr := fs.write(pp, Plist(s, r)); werr != nil {
			return recreated, fmt.Errorf("osadapter: rewrite %s: %w", label, werr)
		}
		if berr := robustReload(c, label, pp, time.Sleep); berr != nil {
			return recreated, fmt.Errorf("osadapter: rebootstrap %s: %w", label, berr)
		}
		recreated = append(recreated, r)
	}
	return recreated, nil
}
