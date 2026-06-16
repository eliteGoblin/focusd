//go:build !darwin

package osadapter

import (
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// TestWatchdogStubsNoOp: on non-darwin the watchdog rail is a compile-only
// no-op (cron is unix-only behind the osadapter seam). Ensure/Refresh/Remove
// return nil and WatchdogStatus reports the rail absent, so cross-platform
// callers stay safe.
func TestWatchdogStubsNoOp(t *testing.T) {
	if err := EnsureWatchdog(mode.User, "/x", "v1.0.0"); err != nil {
		t.Fatalf("EnsureWatchdog stub = %v, want nil", err)
	}
	if err := RefreshWatchdog(mode.User, "/x", "v1.0.0"); err != nil {
		t.Fatalf("RefreshWatchdog stub = %v, want nil", err)
	}
	if err := RemoveWatchdog(mode.User); err != nil {
		t.Fatalf("RemoveWatchdog stub = %v, want nil", err)
	}
	if cron, copyOK := WatchdogStatus(mode.User); cron || copyOK {
		t.Fatalf("WatchdogStatus stub = (%v,%v), want (false,false)", cron, copyOK)
	}
}
