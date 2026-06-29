//go:build !darwin

package osadapter

import "github.com/eliteGoblin/focusd/daemon/internal/mode"

// FEATURE 18 / ADR-0020: the out-of-band companion is launchd/darwin-only. On
// non-darwin these are no-ops so GOOS=linux/windows compile (mirrors
// watchdog_other.go / ctl_other.go). Best-effort callers treat nil as "nothing
// to do"; CompanionStatus reports the rail absent.

func EnsureCompanion(mode.Mode, string, string) error        { return nil }
func RefreshCompanionBackup(mode.Mode, []byte, string) error { return nil }
func RemoveCompanion(mode.Mode) error                        { return nil }
func TouchCompanionHeartbeat(mode.Mode) error                { return nil }

func CompanionStatus(mode.Mode) (present, backupOK bool) { return false, false }
