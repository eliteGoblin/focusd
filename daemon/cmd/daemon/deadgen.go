package main

import "github.com/eliteGoblin/focusd/daemon/internal/core"

// dueEveryTicks reports whether a per-tick-throttled action should fire on the
// given 1-based winning-tick counter: true on the 1st tick (prompt) and every
// `every` ticks after. Mirrors the executor's maybeReapForeign throttle math so
// the steady-state dead-generation retirement (#106-a) shares that cadence. A
// non-positive `every` degrades to "always" (defensive; never used in prod).
func dueEveryTicks(counter, every int) bool {
	return every <= 0 || (counter-1)%every == 0
}

// steadyDeadGenRetireDue advances the winning-tick counter and reports whether the
// steady-state dead-generation retirement (#106-a) should run THIS reconcile tick.
// It is the SINGLE decision point (pure → unit-tested) for the loop wiring:
//
//   - it fires ONLY for the non-test platform-lock HOLDER — a standby (lock loser)
//     never retires (mirroring maybeReapForeign, so two daemons never fight), and
//     test mode is excluded (the retire's mode.SupportRoot(Test,…) would resolve to
//     the REAL ~/Library — the storage-separation hazard the reaper is also gated
//     off for);
//   - it is throttled to core.ReapEveryTicks — the first winning tick (prompt) then
//     once per that many ticks (~10s), the same cadence as the foreign-platform reap.
//
// A non-eligible tick neither advances the counter nor fires, so a daemon that only
// briefly holds the lock still gets a prompt retire on its FIRST winning tick.
func steadyDeadGenRetireDue(isTest, holdsLock bool, ticks int) (fire bool, next int) {
	if isTest || !holdsLock {
		return false, ticks // frozen until eligible
	}
	next = ticks + 1
	return dueEveryTicks(next, core.ReapEveryTicks), next
}
