package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// FEATURE 17 Item 1: the baked fallback platform version MUST be a strict
// semver tag — otherwise build() leaves Executor.Fallback empty and the
// wiped-workdir self-heal silently regresses to the old Blocked behavior.
func TestDefaultPlatformVersionValid(t *testing.T) {
	if !isValidVersionTag(defaultPlatformVersion) {
		t.Fatalf("defaultPlatformVersion %q is not a strict semver tag", defaultPlatformVersion)
	}
}

// FEATURE 17 Item 2: the singleton lock path is FIXED + mode-keyed for
// user/system (survives workdir rotation) and per-workdir for test (e2e
// isolation).
func TestSingletonLockPath(t *testing.T) {
	const wd = "/tmp/some/rotating/workdir"

	// user/system → fixed path under the mode's support root, NOT under wd.
	for _, m := range []mode.Mode{mode.User, mode.System} {
		got := singletonLockPath(m, wd)
		if filepath.Base(got) != fixedSingletonLockName {
			t.Errorf("mode %s: basename = %q, want %q", m, filepath.Base(got), fixedSingletonLockName)
		}
		if strings.HasPrefix(got, wd) {
			t.Errorf("mode %s: lock path %q must NOT live under the rotating workdir", m, got)
		}
		if !strings.Contains(got, "Application Support") {
			t.Errorf("mode %s: lock path %q must live under the support root", m, got)
		}
	}

	// system specifically resolves to the /Library support root.
	if got := singletonLockPath(mode.System, wd); !strings.HasPrefix(got, "/Library/Application Support") {
		t.Errorf("system lock path %q must be under /Library/Application Support", got)
	}

	// test → per-workdir path (matches Store.LockPath, stays inside wd).
	wantTest := (&core.Store{Dir: wd}).LockPath()
	if got := singletonLockPath(mode.Test, wd); got != wantTest {
		t.Errorf("test lock path = %q, want per-workdir %q", got, wantTest)
	}
}
