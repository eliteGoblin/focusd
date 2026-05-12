package policy

import (
	"context"
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// These tests pin Dota 2's blocking *contract* — the patterns it kills
// and the paths it deletes. Easy to silently break by renaming a Valve
// binary; the patterns are the protection.

func TestDota2Policy_IdentityFields(t *testing.T) {
	p := NewDota2Policy()
	if got := p.ID(); got != "dota2" {
		t.Errorf("ID() = %q, want %q", got, "dota2")
	}
	if got := p.Name(); got != "Dota 2" {
		t.Errorf("Name() = %q, want %q", got, "Dota 2")
	}
}

func TestDota2Policy_ProcessPatternsCoverKnownExecutables(t *testing.T) {
	p := NewDota2Policy()
	patterns := p.ProcessPatterns()
	if len(patterns) == 0 {
		t.Fatal("ProcessPatterns() returned empty — Dota 2 wouldn't be killable")
	}

	// All of these are macOS process names observed on real installs;
	// removing any silently breaks the kill loop.
	required := []string{
		"dota2",        // launcher
		"dota_osx64",   // 64-bit game binary
		"Dota 2",       // UI / app bundle name
	}
	have := map[string]struct{}{}
	for _, p := range patterns {
		have[p] = struct{}{}
	}
	for _, want := range required {
		if _, ok := have[want]; !ok {
			t.Errorf("ProcessPatterns missing required entry %q; got %v", want, patterns)
		}
	}
}

func TestDota2Policy_PathsToDelete_AreUnderSteamLibrary(t *testing.T) {
	// Dota 2 installs under Steam's library; if the policy ever produces
	// a path NOT under the configured home, the enforcer would silently
	// fail (file doesn't exist) and leave the install intact.
	const home = "/Users/testuser"
	p := NewDota2PolicyWithHome(home)
	paths := p.PathsToDelete()
	if len(paths) == 0 {
		t.Fatal("PathsToDelete() returned empty")
	}

	steamRoot := home + "/Library/Application Support/Steam"
	for _, path := range paths {
		// "Downloads" globs for Dota installers are a legitimate exception.
		if strings.HasPrefix(path, home+"/Downloads/") {
			continue
		}
		if !strings.HasPrefix(path, steamRoot) {
			t.Errorf("path %q is neither under Steam library nor Downloads — would silently no-op", path)
		}
	}
}

func TestDota2Policy_PathsToDelete_CoverGameInstallAndCache(t *testing.T) {
	// Pin the specific files the policy must remove; otherwise a reinstall
	// would pick up cached state and skip the download.
	const home = "/Users/testuser"
	p := NewDota2PolicyWithHome(home)
	paths := p.PathsToDelete()

	expectedSubstrings := []string{
		"steamapps/common/dota 2 beta",       // main game install
		"steamapps/appmanifest_570.acf",      // Steam catalog entry
		"steamapps/workshop/content/570",     // mods / custom games
		"steamapps/shadercache/570",          // GPU shader cache
		"steamapps/downloading/570",          // partial downloads
	}
	for _, need := range expectedSubstrings {
		found := false
		for _, path := range paths {
			if strings.Contains(path, need) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no path contained substring %q — Dota 2 reinstall could replay this state", need)
		}
	}
}

func TestDota2Policy_ScanInterval_MatchesDefault(t *testing.T) {
	p := NewDota2Policy()
	if got := p.ScanInterval(); got != DefaultScanInterval {
		t.Errorf("ScanInterval() = %v, want %v", got, DefaultScanInterval)
	}
}

// PreEnforce and PostEnforce are currently no-op hooks. Tests pin them as
// non-failing so a future implementation that returns an error doesn't
// silently disable enforcement (Enforce treats hook errors as warnings).
func TestDota2Policy_LifecycleHooks_AreNonFailing(t *testing.T) {
	p := NewDota2Policy()
	ctx := context.Background()
	if err := p.PreEnforce(ctx); err != nil {
		t.Errorf("PreEnforce returned error: %v", err)
	}
	if err := p.PostEnforce(ctx, &domain.EnforcementResult{}); err != nil {
		t.Errorf("PostEnforce returned error: %v", err)
	}
}

// Contract assertion at compile time: Dota2Policy must implement AppPolicy.
// Already asserted in the package via `var _ AppPolicy = ...` but we
// re-check at test time to catch refactor regressions.
func TestDota2Policy_ImplementsAppPolicyInterface(t *testing.T) {
	var _ AppPolicy = NewDota2Policy()
	var _ AppPolicy = NewDota2PolicyWithHome("/x")
}
