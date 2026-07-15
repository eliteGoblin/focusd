package companion

import (
	"strings"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// TestDecideStale: a heartbeat younger than StaleThreshold is fresh (daemon
// alive → no-op); one older is stale; a zero/missing mtime is stale.
func TestDecideStale(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		mtime time.Time
		want  bool
	}{
		{"just touched", now, false},
		{"well within threshold", now.Add(-StaleThreshold / 2), false},
		{"exactly at threshold", now.Add(-StaleThreshold), true},
		{"well past threshold", now.Add(-StaleThreshold - time.Minute), true},
		{"zero mtime (missing heartbeat)", time.Time{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecideStale(tc.mtime, now); got != tc.want {
				t.Fatalf("DecideStale = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDirPathsUnderRootDistinct: every Dir path is under the hidden-dot folder
// root and all the role files are distinct (no two helpers collide).
func TestDirPathsUnderRootDistinct(t *testing.T) {
	d := For(mode.User, "/home/u")
	root := d.Root()
	if !strings.Contains(root, "/.com.apple.") {
		t.Fatalf("root is not a hidden-dot disguised folder: %q", root)
	}
	paths := map[string]string{
		"binary":    d.Binary(),
		"backup":    d.Backup(),
		"desired":   d.Desired(),
		"heartbeat": d.Heartbeat(),
		"promote":   d.Promote(),
		"label":     d.LabelFile(),
		"log":       d.Log(),
		"ranmarker": d.RanMarker(),
	}
	seen := map[string]string{}
	for name, p := range paths {
		if !strings.HasPrefix(p, root+"/") {
			t.Fatalf("%s path %q is not under root %q", name, p, root)
		}
		if other, dup := seen[p]; dup {
			t.Fatalf("%s and %s collide on path %q", name, other, p)
		}
		seen[p] = name
	}
}

// TestDirFromBinaryMatchesFor is the #101 correctness invariant: deriving the
// companion Dir from the companion BINARY's path (HOME-free, mode-free) resolves
// the EXACT same folder For() installed it into — in BOTH user and system modes.
// This is what lets a system LaunchDaemon (no $HOME) re-find its own folder.
func TestDirFromBinaryMatchesFor(t *testing.T) {
	for _, m := range []mode.Mode{mode.User, mode.System} {
		want := For(m, "/home/u")
		got := DirFromBinary(want.Binary())
		if got.Root() != want.Root() {
			t.Fatalf("mode %s: DirFromBinary(%q).Root() = %q, want %q",
				m, want.Binary(), got.Root(), want.Root())
		}
		// Every derived sibling path must match too (the binary's parent IS the root).
		if got.Backup() != want.Backup() || got.Heartbeat() != want.Heartbeat() || got.Desired() != want.Desired() {
			t.Fatalf("mode %s: DirFromBinary sibling paths diverge from For", m)
		}
	}
}

// TestForModeKeyed: user and system installs resolve to DIFFERENT folder roots
// so they never share a companion folder (system → /Library).
func TestForModeKeyed(t *testing.T) {
	u := For(mode.User, "/home/u").Root()
	s := For(mode.System, "/home/u").Root()
	if u == s {
		t.Fatalf("user and system companion roots must differ, both = %q", u)
	}
	if !strings.HasPrefix(s, "/Library/") {
		t.Fatalf("system companion root must be under /Library, got %q", s)
	}
}

// TestIsValidVersion: only pinned semver tags pass — an empty/garbage desired
// must be refused before it reaches `daemon watchdog -v`.
func TestIsValidVersion(t *testing.T) {
	good := []string{"v0.16.3", "v1.2.3", "v1.2.3-rc.1", "v10.0.0+build"}
	bad := []string{"", "latest", "1.2.3", "v", "vlatest", "../etc"}
	for _, g := range good {
		if !IsValidVersion(g) {
			t.Errorf("IsValidVersion(%q) = false, want true", g)
		}
	}
	for _, b := range bad {
		if IsValidVersion(b) {
			t.Errorf("IsValidVersion(%q) = true, want false", b)
		}
	}
}
