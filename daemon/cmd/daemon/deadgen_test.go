package main

import (
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
)

// TestDueEveryTicks pins the throttle math shared with the executor's reap:
// fire on the 1st tick (prompt) and then every `every` ticks.
func TestDueEveryTicks(t *testing.T) {
	every := core.ReapEveryTicks
	var fired []int
	for tick := 1; tick <= 3*every; tick++ {
		if dueEveryTicks(tick, every) {
			fired = append(fired, tick)
		}
	}
	want := []int{1, 1 + every, 1 + 2*every}
	if len(fired) != len(want) {
		t.Fatalf("fired ticks = %v, want %v", fired, want)
	}
	for i := range want {
		if fired[i] != want[i] {
			t.Fatalf("fired ticks = %v, want %v", fired, want)
		}
	}
}

// TestSteadyDeadGenRetireDue (#106-a) verifies the reconcile-loop gate:
//   - a lock LOSER never fires and never advances the counter;
//   - TEST mode never fires (the retire would touch the real support root);
//   - the non-test HOLDER fires on its FIRST winning tick (prompt) and then once per
//     core.ReapEveryTicks (throttled).
func TestSteadyDeadGenRetireDue(t *testing.T) {
	// Lock loser: never fires, counter frozen at 0.
	if fire, next := steadyDeadGenRetireDue(false /*isTest*/, false /*holdsLock*/, 0); fire || next != 0 {
		t.Fatalf("lock loser must never fire (fire=%v next=%d)", fire, next)
	}

	// Test mode holder: never fires, counter frozen.
	if fire, next := steadyDeadGenRetireDue(true /*isTest*/, true /*holdsLock*/, 7); fire || next != 7 {
		t.Fatalf("test mode must never fire (fire=%v next=%d)", fire, next)
	}

	// Non-test holder: prompt on the first winning tick, then throttled.
	ticks := 0
	fireCount := 0
	firstWinTick := -1
	for i := 0; i < 3*core.ReapEveryTicks; i++ {
		var fire bool
		fire, ticks = steadyDeadGenRetireDue(false, true, ticks)
		if fire {
			fireCount++
			if firstWinTick < 0 {
				firstWinTick = i
			}
		}
	}
	if firstWinTick != 0 {
		t.Fatalf("holder must retire PROMPTLY on its first winning tick, first fire at loop %d", firstWinTick)
	}
	if fireCount != 3 {
		t.Fatalf("holder must fire once per ReapEveryTicks (3 times over 3 windows), got %d", fireCount)
	}
	if ticks != 3*core.ReapEveryTicks {
		t.Fatalf("counter must advance once per winning tick, got %d", ticks)
	}
}
