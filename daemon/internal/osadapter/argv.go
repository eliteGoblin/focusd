package osadapter

import (
	"path/filepath"
	"strings"
	"time"
)

// This file holds the pure, OS-agnostic argv/label helpers used by both the
// darwin controller (FindCurrentInstall / self-update) and the cross-platform
// unit tests. They live here — NOT in ctl_darwin.go — so the untagged tests
// (manager_test.go, find_install_test.go, selfupdate_test.go) compile on
// Linux/Windows too. (CI: `undefined: sameRoster` on ubuntu.)

// rosterFromArgv pulls the comma-joined "--roster" value out of a parsed
// argv and splits it into the mesh-label set (FEATURE 10 / ADR-0014).
// Returns nil when the flag is absent or empty. This is the correlation
// key FindCurrentInstall uses now that the three labels share no base.
func rosterFromArgv(argv []string) []string {
	var raw string
	for i, a := range argv {
		if a == "--roster" && i+1 < len(argv) {
			raw = argv[i+1]
			break
		}
		if strings.HasPrefix(a, "--roster=") {
			raw = strings.TrimPrefix(a, "--roster=")
			break
		}
	}
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

// sameRoster reports whether two roster label sets are identical in order
// and content — the agreement check that ties three plists to one install.
func sameRoster(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// workdirFromArgv pulls the value following "--workdir" out of a parsed
// argv. Returns "" when the flag is absent.
func workdirFromArgv(argv []string) string {
	for i, a := range argv {
		if a == "--workdir" && i+1 < len(argv) {
			return argv[i+1]
		}
		if strings.HasPrefix(a, "--workdir=") {
			return strings.TrimPrefix(a, "--workdir=")
		}
	}
	return ""
}

// WorkdirFromBinary recovers the mesh workdir from the daemon's own
// binary path (FEATURE 14 / ADR-0018). The disguised binary is relocated
// INSIDE the workdir, so its parent directory IS the workdir — this lets a
// minimized-argv mesh process find its workdir (and thus the masked roster)
// without carrying --workdir on the command line. Returns "" for an empty
// input, a relative path, or a root-level binary so the caller can fall back
// to the default workdir: filepath.Dir yields "." for a relative path and "/"
// for a root path — both non-empty, which would otherwise short-circuit the
// caller's fallback into a nonsensical workdir.
func WorkdirFromBinary(self string) string {
	if self == "" {
		return ""
	}
	parent := filepath.Dir(self)
	if !filepath.IsAbs(parent) || parent == "/" {
		return ""
	}
	return parent
}

// hasMeshFlag reports whether a parsed argv carries the "--mesh" marker —
// the corroborating signal (alongside Ed25519 verification of argv[0]) that a
// plist belongs to a focusd self-healing mesh worker (FEATURE 17, generation
// cleanup). The ensure role's argv (`ensure`) does NOT carry --mesh; a
// generation is recognised when its VERIFIED binary has at least one --mesh
// plist, and all plists sharing that binary are then grouped into it.
func hasMeshFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--mesh" {
			return true
		}
	}
	return false
}

// safeToRemoveWorkdir reports whether dir is a safe os.RemoveAll target during
// generation cleanup (FEATURE 17, Item 3). The teardown deletes an OLD
// generation's workdir, so a malformed/disguised value must never widen the
// blast radius. A dir is safe ONLY when ALL hold:
//   - it is a non-empty, absolute path;
//   - it is STRICTLY nested under supportRoot (never the root itself, never
//     outside it) — so a bug can't delete "/Library/Application Support" or a
//     sibling tree;
//   - it is NOT the keep generation's workdir; and
//   - it is NOT an ancestor of the keep workdir (deleting an ancestor would
//     take the surviving install — and the out-of-band watchdog dir, which is
//     a sibling under the same root — down with it).
//
// Pure → unit-tested on Linux CI.
func safeToRemoveWorkdir(dir, supportRoot, keepWorkdir string) bool {
	if dir == "" || supportRoot == "" || !filepath.IsAbs(dir) {
		return false
	}
	dir = filepath.Clean(dir)
	root := filepath.Clean(supportRoot)

	// Must be strictly under root: a valid relative path that is neither "."
	// (== root) nor an escape ("..").
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}

	if keepWorkdir != "" {
		keep := filepath.Clean(keepWorkdir)
		if dir == keep {
			return false // never delete the surviving generation's workdir
		}
		// Reject dir being an ancestor of keep (would delete keep too).
		if kr, kerr := filepath.Rel(dir, keep); kerr == nil &&
			kr != ".." && !strings.HasPrefix(kr, ".."+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

// intervalFromArgv pulls the reconcile interval following "--interval" out of
// a parsed argv. Returns 0 when the flag is absent or unparseable — caller
// substitutes a default.
func intervalFromArgv(argv []string) time.Duration {
	var raw string
	for i, a := range argv {
		if a == "--interval" && i+1 < len(argv) {
			raw = argv[i+1]
			break
		}
		if strings.HasPrefix(a, "--interval=") {
			raw = strings.TrimPrefix(a, "--interval=")
			break
		}
	}
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0
	}
	return d
}
