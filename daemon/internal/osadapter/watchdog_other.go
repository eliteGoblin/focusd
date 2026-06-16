//go:build !darwin

package osadapter

import "github.com/eliteGoblin/focusd/daemon/internal/mode"

// FEATURE 12 / ADR-0016: the out-of-band watchdog rail is cron-based, hence
// unix/darwin-only. On non-darwin these are no-ops so GOOS=linux/windows
// compile (mirrors ctl_other.go). Ensure/Refresh/Remove return nil
// (best-effort callers treat that as "nothing to do"); WatchdogStatus reports
// the rail as absent.

func EnsureWatchdog(mode.Mode, string, string) error  { return nil }
func RefreshWatchdog(mode.Mode, string, string) error { return nil }
func RemoveWatchdog(mode.Mode) error                  { return nil }

func WatchdogStatus(mode.Mode) (cronPresent, copyOK bool) { return false, false }
