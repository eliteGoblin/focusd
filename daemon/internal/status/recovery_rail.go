package status

// recoveryRailStatus folds the two out-of-band recovery rails into the single
// (railPresent, copyOK) pair the Snapshot reports.
//
// FEATURE 18 / ADR-0020's launchd COMPANION SUPERSEDED FEATURE 12's cron
// watchdog as the recovery rail, so the companion is authoritative: whenever it
// is present OR its signed daemon backup verifies, report the companion. Only
// when the companion rail is ENTIRELY absent — a pre-F18 install still on the
// legacy cron rail — do we fall back to the cron watchdog, so the status never
// goes dark mid-migration.
//
// This is the fix for the v0.18.0 live GAP 2: `daemon status` used to source
// `watchdog_copy_ok` from the cron watchdog ALONE, which a companion install no
// longer carries — so the field read false on every companion install even
// though the companion's signed backup verified fine. Reporting the companion
// makes the field reflect the rail that actually exists.
//
// Pure — unit-tested. railPresent tracks whether the companion rail is genuinely
// UP: its binary/job is present (companionPresent) AND it is actually FIRING
// (companionRan — the RanMarker shows a recent completed pass, issue #status-2).
// A companion that is on-disk-and-loaded but never completes a pass (the #101
// no-$HOME DOA class) reports companionRan=false → railPresent=false → status
// omits the line, honestly signalling a dead rail instead of trusting presence.
// copyOK tracks whether its signed backup verifies (companionBackupOK) — so a
// placeholder-embed build with a verified backup but no companion binary honestly
// reports copyOK=true, railPresent=false.
func recoveryRailStatus(companionPresent, companionBackupOK, companionRan, cronPresent, cronCopyOK bool) (railPresent, copyOK bool) {
	if companionPresent || companionBackupOK {
		return companionPresent && companionRan, companionBackupOK
	}
	return cronPresent, cronCopyOK
}
