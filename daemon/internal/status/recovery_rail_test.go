package status

import "testing"

// GAP 2 (v0.18.0 live): `daemon status` reported watchdog_copy_ok=false on
// every companion install because it read the superseded cron-watchdog rail,
// never the companion's (verifying) signed backup. recoveryRailStatus now
// reports the companion when present/verified and only falls back to the cron
// rail for a pre-F18 install — so a healthy companion drives watchdog_copy_ok
// TRUE.
func TestRecoveryRailStatus(t *testing.T) {
	tests := []struct {
		name                                             string
		cPresent, cBackupOK, cRan, cronPresent, cronCopy bool
		wantPresent, wantCopyOK                          bool
	}{
		{
			name:     "companion present + firing + backup verifies ⇒ report companion (GAP-2 fix)",
			cPresent: true, cBackupOK: true, cRan: true,
			cronPresent: false, cronCopy: false,
			wantPresent: true, wantCopyOK: true,
		},
		{
			// issue #status-2: on disk + loaded but NOT firing (the #101 DOA class)
			// → railPresent false so status omits the line (honest dead-rail read).
			name:     "companion present but NOT firing ⇒ railPresent false (dead-rail honesty)",
			cPresent: true, cBackupOK: true, cRan: false,
			cronPresent: true, cronCopy: true, // cron ignored — companion is authoritative
			wantPresent: false, wantCopyOK: true,
		},
		{
			name:     "companion present + firing but backup fails ⇒ copy_ok false honestly",
			cPresent: true, cBackupOK: false, cRan: true,
			cronPresent: true, cronCopy: true, // cron ignored — companion is authoritative
			wantPresent: true, wantCopyOK: false,
		},
		{
			name:     "placeholder build: verified backup, no companion binary ⇒ copy_ok true, present false",
			cPresent: false, cBackupOK: true, cRan: false,
			cronPresent: true, cronCopy: true, // cron ignored — companion backup present
			wantPresent: false, wantCopyOK: true,
		},
		{
			name:     "companion entirely absent ⇒ fall back to the legacy cron rail",
			cPresent: false, cBackupOK: false, cRan: false,
			cronPresent: true, cronCopy: true,
			wantPresent: true, wantCopyOK: true,
		},
		{
			name:     "nothing installed ⇒ all false",
			cPresent: false, cBackupOK: false, cRan: false,
			cronPresent: false, cronCopy: false,
			wantPresent: false, wantCopyOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			present, copyOK := recoveryRailStatus(tt.cPresent, tt.cBackupOK, tt.cRan, tt.cronPresent, tt.cronCopy)
			if present != tt.wantPresent || copyOK != tt.wantCopyOK {
				t.Fatalf("recoveryRailStatus = (present=%v, copyOK=%v), want (present=%v, copyOK=%v)",
					present, copyOK, tt.wantPresent, tt.wantCopyOK)
			}
		})
	}
}
