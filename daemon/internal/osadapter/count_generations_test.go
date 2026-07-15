//go:build darwin

package osadapter

import (
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// TestCountOtherGenerations_Pure drives the read-only count core over its full
// matrix with plain slices (no launchd / FS scan), mirroring how the retire
// tests exercise retireGenerations. An "other" generation is a live generation
// whose binary differs from the keep, PLUS every dead generation.
func TestCountOtherGenerations_Pure(t *testing.T) {
	keep := "/Library/Application Support/.keep/keep.bin"
	other := "/Library/Application Support/.old/old.bin"
	cases := []struct {
		name string
		live []Generation
		dead []DeadGeneration
		keep string
		want int
	}{
		{
			name: "keep only → 0 (clean)",
			live: []Generation{{BinaryPath: keep}},
			want: 0,
		},
		{
			name: "keep + one live other → 1",
			live: []Generation{{BinaryPath: keep}, {BinaryPath: other}},
			want: 1,
		},
		{
			name: "keep + one dead → 1",
			live: []Generation{{BinaryPath: keep}},
			dead: []DeadGeneration{{BinaryPath: other}},
			want: 1,
		},
		{
			name: "keep + one live other + two dead → 3",
			live: []Generation{{BinaryPath: keep}, {BinaryPath: other}},
			dead: []DeadGeneration{{BinaryPath: "/x/a.bin"}, {BinaryPath: "/x/b.bin"}},
			want: 3,
		},
		{
			name: "non-canonical keep still matches the keep generation → 0",
			live: []Generation{{BinaryPath: keep}},
			keep: keep + "/", // trailing slash; Clean must normalise before compare
			want: 0,
		},
		{
			name: "no generations at all → 0",
			want: 0,
		},
		{
			name: "dead entry equal to keep is guarded out → 0 (defensive, mirrors retire)",
			dead: []DeadGeneration{{BinaryPath: keep}},
			want: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := c.keep
			if k == "" {
				k = keep
			}
			if got := countOtherGenerations(c.live, c.dead, k); got != c.want {
				t.Fatalf("countOtherGenerations = %d; want %d", got, c.want)
			}
		})
	}
}

// TestCountOtherGenerations_ViaDiscovery exercises the full read-only path over
// a temp support-root fixture with two live generations (one is the keep, one an
// orphan) plus a dead/zombie generation: DiscoverAllGenerations (real scan, fake
// verifier) → countOtherGenerations must report exactly the two non-keep
// generations, and never retire anything (the fixture is untouched afterward).
func TestCountOtherGenerations_ViaDiscovery(t *testing.T) {
	home, laDir := laDirUnderHome(t)

	keepRoster := []string{"com.apple.metadata.helper.1", "com.google.keystone.daemon.2", "org.mozilla.updater.agent.3"}
	otherRoster := []string{"com.docker.helper.4", "us.zoom.ZoomDaemon.svc.5", "io.tailscale.ipnextension.relay.6"}
	deadRoster := []string{"com.slack.helper.7", "com.spotify.client.8", "com.dropbox.agent.9"}

	keepBin, _ := writeGeneration(t, home, laDir, "keep", keepRoster)
	writeGeneration(t, home, laDir, "other", otherRoster) // a live ORPHAN generation
	writeDeadGeneration(t, home, laDir, "dead", deadRoster, AllRoles...)

	live, dead, err := DiscoverAllGenerations(mode.User, Verifier(readFileVerifier))
	if err != nil {
		t.Fatalf("DiscoverAllGenerations: %v", err)
	}
	// Sanity: the fixture yields 2 live (keep + other) and 1 dead generation.
	if len(live) != 2 || len(dead) != 1 {
		t.Fatalf("fixture: live=%d dead=%d; want 2 live, 1 dead", len(live), len(dead))
	}

	if got := countOtherGenerations(live, dead, keepBin); got != 2 {
		t.Fatalf("countOtherGenerations(keep) = %d; want 2 (1 live orphan + 1 dead)", got)
	}
}

// TestCountOtherGenerations_EmptyKeepErrors pins the guard: an empty keep would
// make every generation "other", so it must ERROR (status folds to unknown)
// rather than answer a meaningless count.
func TestCountOtherGenerations_EmptyKeepErrors(t *testing.T) {
	if _, err := CountOtherGenerations(mode.User, ""); err == nil {
		t.Fatal("empty keepBinaryPath must return an error")
	}
}
