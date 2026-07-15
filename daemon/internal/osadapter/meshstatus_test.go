package osadapter

import "testing"

// TestMeshStatusCounts is the issue #status-1 regression guard: the mesh total is
// the EXPECTED set size (always 3 for a discovered focusd mesh), NOT the count of
// plists found on disk. A lost member must read N/3 (→ DEGRADED/DOWN), never
// "2/2 HEALTHY".
func TestMeshStatusCounts(t *testing.T) {
	roster := []string{"com.vendor.alpha", "com.vendor.bravo", "com.vendor.charlie"}

	// loadedSet builds a loadedFn that reports only the listed labels loaded.
	loadedSet := func(loaded ...string) func(string) bool {
		m := map[string]bool{}
		for _, l := range loaded {
			m[l] = true
		}
		return func(l string) bool { return m[l] }
	}

	cases := []struct {
		name       string
		cur        CurInstall
		loadedFn   func(string) bool
		wantLoaded int
		wantTotal  int
		wantFound  bool
	}{
		{
			name:       "full roster, all 3 loaded ⇒ 3/3",
			cur:        CurInstall{Labels: roster, Roster: roster},
			loadedFn:   loadedSet(roster...),
			wantLoaded: 3, wantTotal: 3, wantFound: true,
		},
		{
			// The core fix: a lost member drops its plist, so only 2 labels are
			// FOUND — but the roster still names all 3, so status must read 2/3.
			name:       "lost member (2 plists) but full roster ⇒ 2/3 (was 2/2)",
			cur:        CurInstall{Labels: roster[:2], Roster: roster},
			loadedFn:   loadedSet(roster[0], roster[1]),
			wantLoaded: 2, wantTotal: 3, wantFound: true,
		},
		{
			name:       "all roster labels present but none loaded ⇒ 0/3 (DOWN)",
			cur:        CurInstall{Labels: roster, Roster: roster},
			loadedFn:   loadedSet(),
			wantLoaded: 0, wantTotal: 3, wantFound: true,
		},
		{
			// No full roster (old/degraded discovery): expected set is the found
			// labels, but total is still len(AllRoles) — a focusd mesh is always 3.
			name:       "no full roster, 1 found + loaded ⇒ 1/3",
			cur:        CurInstall{Labels: roster[:1]},
			loadedFn:   loadedSet(roster[0]),
			wantLoaded: 1, wantTotal: 3, wantFound: true,
		},
		{
			name:       "nothing found ⇒ 0/0 not found",
			cur:        CurInstall{},
			loadedFn:   loadedSet(),
			wantLoaded: 0, wantTotal: 0, wantFound: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			loaded, total, found := meshStatusCounts(c.cur, c.loadedFn)
			if loaded != c.wantLoaded || total != c.wantTotal || found != c.wantFound {
				t.Fatalf("meshStatusCounts = (%d,%d,%v), want (%d,%d,%v)",
					loaded, total, found, c.wantLoaded, c.wantTotal, c.wantFound)
			}
		})
	}
}
