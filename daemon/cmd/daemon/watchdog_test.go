package main

import (
	"errors"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/osadapter"
)

// fullMesh is a CurInstall with all three role plists present — the "healthy"
// shape meshComplete must accept.
func fullMesh() osadapter.CurInstall {
	return osadapter.CurInstall{
		PlistPaths: make([]string, len(osadapter.AllRoles)),
	}
}

// TestMeshComplete is the pure health predicate the watchdog acts on
// (acceptance #1): only a discovery-error-free, all-roles-present mesh is
// "complete"; anything less needs a rebuild.
func TestMeshComplete(t *testing.T) {
	cases := []struct {
		name string
		cur  osadapter.CurInstall
		err  error
		want bool
	}{
		{"all three roles present", fullMesh(), nil, true},
		{"discovery error", fullMesh(), errors.New("io"), false},
		{"no install", osadapter.CurInstall{}, nil, false},
		{
			"incomplete mesh (2 of 3)",
			osadapter.CurInstall{PlistPaths: []string{"/a", "/b"}},
			nil,
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := meshComplete(tc.cur, tc.err); got != tc.want {
				t.Fatalf("meshComplete = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRunWatchdogHealthyMeshNoInstall: a complete mesh (3 plists, via the
// FindCurrentInstall fake) → the install path is NOT called (acceptance: the
// watchdog is a quiet no-op when protection is intact).
func TestRunWatchdogHealthyMeshNoInstall(t *testing.T) {
	installed := false
	code := runWatchdog("/copy/bin", mode.User, "v1.0.0",
		func() (osadapter.CurInstall, error) { return fullMesh(), nil },
		func(*osadapter.Spec) error { installed = true; return nil },
	)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if installed {
		t.Fatalf("install was called for a healthy mesh; want NOT called")
	}
}

// TestRunWatchdogTotalTeardownRebuilds: an absent mesh (total teardown) → the
// install path IS invoked locally, with the pinned version + a mode-matched
// spec (acceptance #1: rebuild after total teardown).
func TestRunWatchdogTotalTeardownRebuilds(t *testing.T) {
	var gotSpec *osadapter.Spec
	code := runWatchdog("/copy/bin", mode.System, "v2.3.4",
		func() (osadapter.CurInstall, error) { return osadapter.CurInstall{}, nil },
		func(s *osadapter.Spec) error { gotSpec = s; return nil },
	)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if gotSpec == nil {
		t.Fatalf("install was NOT called for an absent mesh; want called")
	}
	if gotSpec.Mode != mode.System {
		t.Fatalf("rebuild mode = %q, want system (mode-matched)", gotSpec.Mode)
	}
	if gotSpec.SelfPath != "/copy/bin" {
		t.Fatalf("rebuild SelfPath = %q, want the watchdog copy path", gotSpec.SelfPath)
	}
}

// TestRunWatchdogIncompleteMeshRebuilds: a partial mesh (a non-total wipe that
// still left the watchdog deciding) also triggers a rebuild.
func TestRunWatchdogIncompleteMeshRebuilds(t *testing.T) {
	installed := false
	code := runWatchdog("/copy/bin", mode.User, "v1.0.0",
		func() (osadapter.CurInstall, error) {
			return osadapter.CurInstall{PlistPaths: []string{"/only/one"}}, nil
		},
		func(*osadapter.Spec) error { installed = true; return nil },
	)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !installed {
		t.Fatalf("install was NOT called for an incomplete mesh; want called")
	}
}

// TestRunWatchdogRebuildFailureNonZero: a failed local rebuild surfaces a
// non-zero exit (cron ignores it, but the behaviour is honest).
func TestRunWatchdogRebuildFailureNonZero(t *testing.T) {
	code := runWatchdog("/copy/bin", mode.User, "v1.0.0",
		func() (osadapter.CurInstall, error) { return osadapter.CurInstall{}, nil },
		func(*osadapter.Spec) error { return errors.New("rebuild boom") },
	)
	if code == 0 {
		t.Fatalf("exit = 0 for a failed rebuild, want non-zero")
	}
}
