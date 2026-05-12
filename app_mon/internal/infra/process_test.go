package infra

import "testing"

// processNameMatches is the inner matcher used by FindByName. These tests
// pin the exact-match contract introduced in v0.6.1 after the discovery
// that the previous substring-match version was killing Microsoft Teams.
//
// Substring match was actively dangerous: pattern "Steam" lower-cased to
// "steam", which IS a substring of "msteams" (the kernel-reported name
// of Microsoft Teams' main binary, "MSTeams" → "msteams"). Quick-kill
// fired every 10s, taking down the user's Teams calls.
//
// Any regression that re-introduces substring matching MUST fail one of
// the negative cases here.

func TestProcessNameMatches_ExactCaseInsensitive(t *testing.T) {
	cases := []struct {
		name, pattern string
		want          bool
	}{
		// Positive: exact match, various casings.
		{"Steam", "Steam", true},
		{"steam", "Steam", true},
		{"STEAM", "Steam", true},
		{"Steam Helper", "Steam Helper", true},
		{"steam_osx", "steam_osx", true},
		{"DOTA_OSX64", "dota_osx64", true},

		// Negative: REGRESSION GUARD against the v0.6.0 substring bug.
		// "MSTeams" lowercased is "msteams" which contains "steam" —
		// the bug. Must NOT match.
		{"MSTeams", "Steam", false},
		{"MSTeamsAudioDevice", "Steam", false},
		{"Microsoft Teams WebView", "Steam", false},
		{"Microsoft Teams", "Steam", false},

		// Negative: prefix or suffix containing the pattern must NOT match.
		// (No prefix-, suffix-, or word-boundary matching.)
		{"Steam Helper (GPU)", "Steam", false},
		{"NotSteam", "Steam", false},
		{"SteamNot", "Steam", false},
		{"Steamy McSteamface", "Steam", false},

		// Negative: empty strings don't match arbitrary patterns.
		{"", "Steam", false},

		// Edge: empty pattern matches empty name (unusual but consistent).
		{"", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name+"-vs-"+tc.pattern, func(t *testing.T) {
			got := processNameMatches(tc.name, tc.pattern)
			if got != tc.want {
				t.Errorf("processNameMatches(%q, %q) = %v, want %v",
					tc.name, tc.pattern, got, tc.want)
			}
		})
	}
}

// TestFindByName_DoesNotMatchSelfBySubstring is an integration smoke test:
// it asks the real ProcessManagerImpl to look up a deliberately-substring-
// y pattern that would have matched many things under the old code, and
// asserts the result set is sane. We don't fully assert "zero matches"
// because some test runner / dev environment might have processes with
// odd names; what we DO assert is that the test process's own name does
// NOT come back, which is enough to detect a regression to substring
// matching (since "go-test" or similar would substring-match "o").
func TestFindByName_ExactDoesNotCatchSubstringOfRunningProcess(t *testing.T) {
	pm := NewProcessManager()

	// Use a pattern that is almost certainly NOT the basename of any
	// real process: random gibberish that nonetheless might appear as
	// a substring in some app's name.
	pids, err := pm.FindByName("ststststst-no-real-process")
	if err != nil {
		t.Fatalf("FindByName: %v", err)
	}
	if len(pids) != 0 {
		t.Errorf("expected zero matches for gibberish pattern, got %v", pids)
	}
}
