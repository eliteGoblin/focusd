package main

import (
	"errors"
	"strings"
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

// okVerify accepts every binary (the genuine, signed-copy case).
func okVerify(string) (bool, error) { return true, nil }

// failVerify REJECTS the binary (the swapped-copy case): the watchdog must
// refuse to reinstall.
func failVerify(string) (bool, error) { return false, nil }

// TestRunWatchdogHealthyMeshNoInstall: a complete mesh (3 plists, via the
// FindCurrentInstall fake) → the install path is NOT called (acceptance: the
// watchdog is a quiet no-op when protection is intact). The verify step is not
// even reached on the healthy path (no rebuild → nothing to trust).
func TestRunWatchdogHealthyMeshNoInstall(t *testing.T) {
	installed := false
	verifyCalls := 0
	code := runWatchdog("/copy/bin", mode.User, "v1.0.0",
		func(string) (bool, error) { verifyCalls++; return true, nil },
		func() (osadapter.CurInstall, error) { return fullMesh(), nil },
		func(*osadapter.Spec) error { installed = true; return nil },
	)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if installed {
		t.Fatalf("install was called for a healthy mesh; want NOT called")
	}
	if verifyCalls != 0 {
		t.Fatalf("verify ran %d times on the healthy no-op path; want 0", verifyCalls)
	}
}

// TestRunWatchdogVerifyFailNoInstall (HIGH-2): when the mesh needs a rebuild
// but the running copy FAILS Ed25519 verification (a swapped binary), the
// watchdog must NOT reinstall and must exit non-zero. This closes the
// swap-the-copy-between-fires → arbitrary-root-code hole.
func TestRunWatchdogVerifyFailNoInstall(t *testing.T) {
	installed := false
	code := runWatchdog("/copy/bin", mode.System, "v1.0.0",
		failVerify,
		func() (osadapter.CurInstall, error) { return osadapter.CurInstall{}, nil },
		func(*osadapter.Spec) error { installed = true; return nil },
	)
	if installed {
		t.Fatalf("install ran despite a failed signature verification; want NOT called")
	}
	if code == 0 {
		t.Fatalf("exit = 0 after a verify failure, want non-zero")
	}
}

// TestRunWatchdogVerifyErrorNoInstall: a verifier that ERRORS (I/O failure
// reading the copy) is treated the same as a failed verification — refuse to
// reinstall rather than trust an unverifiable binary.
func TestRunWatchdogVerifyErrorNoInstall(t *testing.T) {
	installed := false
	code := runWatchdog("/copy/bin", mode.System, "v1.0.0",
		func(string) (bool, error) { return false, errors.New("read boom") },
		func() (osadapter.CurInstall, error) { return osadapter.CurInstall{}, nil },
		func(*osadapter.Spec) error { installed = true; return nil },
	)
	if installed {
		t.Fatalf("install ran despite a verifier error; want NOT called")
	}
	if code == 0 {
		t.Fatalf("exit = 0 after a verifier error, want non-zero")
	}
}

// TestRunWatchdogTotalTeardownRebuilds: an absent mesh (total teardown) → the
// install path IS invoked locally, with the pinned version + a mode-matched
// spec (acceptance #1: rebuild after total teardown).
func TestRunWatchdogTotalTeardownRebuilds(t *testing.T) {
	var gotSpec *osadapter.Spec
	code := runWatchdog("/copy/bin", mode.System, "v2.3.4",
		okVerify,
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
		okVerify,
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

// TestRunWatchdogRebuildSpecIsProdPlist locks TC-23: the watchdog's LOCAL
// rebuild must render FEATURE-19 PROD plists — the role/mesh marker rides in
// EnvironmentVariables, NOT argv — so a watchdog/companion-driven rebuild can
// never re-leak `run --r <role> --mesh` into `ps` (the leak TC-23 caught). The
// watchdog resolves a REAL deployment mode (user/system, never test) via
// mode.Resolve, so Spec.isTest() is false and args() is nil. We capture the
// EXACT spec the watchdog hands to installMesh and assert the rendered plist's
// ProgramArguments is the binary ALONE for every role (the env marker, which
// `ps` does not display, carries the role). Both real modes are covered: the
// companion runs the watchdog as the user (LaunchAgent) or as root (LaunchDaemon).
func TestRunWatchdogRebuildSpecIsProdPlist(t *testing.T) {
	for _, m := range []mode.Mode{mode.User, mode.System} {
		t.Run(string(m), func(t *testing.T) {
			var gotSpec *osadapter.Spec
			runWatchdog("/copy/bin", m, "v1.2.3",
				okVerify,
				func() (osadapter.CurInstall, error) { return osadapter.CurInstall{}, nil },
				func(s *osadapter.Spec) error { gotSpec = s; return nil },
			)
			if gotSpec == nil {
				t.Fatal("watchdog did not call install for an absent mesh")
			}
			if gotSpec.Mode == mode.Test {
				t.Fatalf("watchdog rebuild spec is test mode → would re-leak full argv")
			}
			for _, r := range osadapter.AllRoles {
				pa := progArgsBlock(t, osadapter.Plist(*gotSpec, r))
				for _, leak := range []string{"--mesh", "<string>run</string>", "<string>ensure</string>"} {
					if strings.Contains(pa, leak) {
						t.Fatalf("%s ProgramArguments leaks %q (TC-23 regression):\n%s", r, leak, pa)
					}
				}
			}
		})
	}
}

// progArgsBlock extracts the <key>ProgramArguments</key><array>…</array>
// substring — what `ps` surfaces — so a leak scan ignores the env dict (whose
// value legitimately holds "run:a"/"ensure").
func progArgsBlock(t *testing.T, plist string) string {
	t.Helper()
	const head = "<key>ProgramArguments</key><array>"
	i := strings.Index(plist, head)
	if i < 0 {
		t.Fatalf("plist has no ProgramArguments:\n%s", plist)
	}
	rest := plist[i:]
	end := strings.Index(rest, "</array>")
	if end < 0 {
		t.Fatalf("plist ProgramArguments not closed:\n%s", plist)
	}
	return rest[:end]
}

// TestRunWatchdogRebuildFailureNonZero: a failed local rebuild surfaces a
// non-zero exit (cron ignores it, but the behaviour is honest).
func TestRunWatchdogRebuildFailureNonZero(t *testing.T) {
	code := runWatchdog("/copy/bin", mode.User, "v1.0.0",
		okVerify,
		func() (osadapter.CurInstall, error) { return osadapter.CurInstall{}, nil },
		func(*osadapter.Spec) error { return errors.New("rebuild boom") },
	)
	if code == 0 {
		t.Fatalf("exit = 0 for a failed rebuild, want non-zero")
	}
}
