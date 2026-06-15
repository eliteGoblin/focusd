package osadapter

import "fmt"

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
		_ = c.bootout(label)
		if err := c.bootstrap(pp); err != nil {
			return fmt.Errorf("osadapter: bootstrap %s: %w", label, err)
		}
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
		_ = c.bootout(label)
		if berr := c.bootstrap(pp); berr != nil {
			return recreated, fmt.Errorf("osadapter: rebootstrap %s: %w", label, berr)
		}
		recreated = append(recreated, r)
	}
	return recreated, nil
}
