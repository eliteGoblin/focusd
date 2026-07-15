package osadapter

// meshStatusCounts is the pure core of MeshStatus (issue #status-1), split out so
// the "N of 3" logic is unit-tested on Linux CI without a real launchctl. loadedFn
// is the launchd-loaded probe (real launchctl in production, a fake in tests).
//
// The bug it fixes: the old MeshStatus reported total = len(cur.Labels) — the
// number of plists it FOUND. A lost mesh member leaves only 2 plists on disk, so
// the status read "2/2 HEALTHY" and hid a degraded mesh. The correct EXPECTED set
// is the full roster:
//   - a genuine focusd mesh is ALWAYS the 3 AllRoles, so once any install is found
//     the total is len(AllRoles); a member that dropped its plist reads as missing
//     from that expected set (2/3 → DEGRADED, 0/3 → DOWN).
//   - the expected LABELS are cur.Roster when the roster carries the full mesh (the
//     roster survives a lost plist, so it is the authoritative "should be running"
//     set); otherwise (old/degraded discovery with no full roster) fall back to the
//     labels actually found.
//
// found = len(cur.Labels) > 0 — an install was discovered at all. loaded counts how
// many of the EXPECTED labels are actually loaded.
func meshStatusCounts(cur CurInstall, loadedFn func(string) bool) (loaded, total int, found bool) {
	found = len(cur.Labels) > 0
	if !found {
		return 0, 0, false
	}
	expected := cur.Labels
	if len(cur.Roster) == len(AllRoles) {
		expected = cur.Roster
	}
	for _, lbl := range expected {
		if loadedFn(lbl) {
			loaded++
		}
	}
	return loaded, len(AllRoles), true
}
