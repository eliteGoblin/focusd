package osadapter

import (
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
