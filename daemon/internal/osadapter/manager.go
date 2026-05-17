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

// installAll writes + (re)loads all three mesh entries (idempotent:
// any stale instance is booted out first).
func installAll(s Spec, c controller, fs fsio) error {
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

// uninstallAll boots out and removes all three entries (idempotent).
func uninstallAll(s Spec, c controller, fs fsio) error {
	var first error
	for _, r := range AllRoles {
		label := s.Label(r)
		_ = c.bootout(label)
		if err := fs.remove(fs.plistPath(label)); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ensureAll recreates any missing entry — the mutual self-healing the
// mesh relies on (a survivor rebuilds dead siblings). Idempotent.
func ensureAll(s Spec, c controller, fs fsio) (recreated []Role, err error) {
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
