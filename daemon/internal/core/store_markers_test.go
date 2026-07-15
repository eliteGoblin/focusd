package core

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSaltDerivedMarkerBasenamesDistinctAndStable pins the invariant every
// FEATURE-26 marker reader relies on: for one daemon-home salt, RosterPath /
// PidFilePath / LockPath resolve to DISTINCT, salt-derived basenames (so no marker
// overwrites another) and are STABLE across reads (so two daemon roles keyed by
// the same daemon-home always agree — a lock/pointer mismatch would mean twin
// platforms or workdir churn).
func TestSaltDerivedMarkerBasenamesDistinctAndStable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, InstallSaltFile), []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Store{Dir: dir}

	roster := s.RosterPath()
	pid := s.PidFilePath()
	lock := s.LockPath()

	// Distinct basenames.
	names := map[string]string{"roster": filepath.Base(roster), "pidfile": filepath.Base(pid), "lock": filepath.Base(lock)}
	seen := map[string]string{}
	for purpose, base := range names {
		if other, dup := seen[base]; dup {
			t.Fatalf("%s and %s collide on basename %q", purpose, other, base)
		}
		seen[base] = purpose
		// De-patterned: must not be the obvious legacy literal.
		if base == RosterFile || base == PlatformPidFile || base == legacyLockFile {
			t.Fatalf("%s basename %q is a legacy literal, expected salt-derived", purpose, base)
		}
	}

	// Stable across reads (a second Store over the same daemon-home agrees).
	s2 := &Store{Dir: dir}
	if s2.RosterPath() != roster || s2.PidFilePath() != pid || s2.LockPath() != lock {
		t.Fatal("marker basenames must be stable for a fixed salt (roles must agree)")
	}
}

// TestNoSaltMarkersUseLegacyBasenames: without a salt the markers fall back to the
// fixed legacy literals (dev/test/e2e determinism).
func TestNoSaltMarkersUseLegacyBasenames(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if filepath.Base(s.RosterPath()) != RosterFile {
		t.Fatalf("no-salt roster = %q, want %q", filepath.Base(s.RosterPath()), RosterFile)
	}
	if filepath.Base(s.PidFilePath()) != PlatformPidFile {
		t.Fatalf("no-salt pidfile = %q, want %q", filepath.Base(s.PidFilePath()), PlatformPidFile)
	}
	if filepath.Base(s.LockPath()) != legacyLockFile {
		t.Fatalf("no-salt lock = %q, want %q", filepath.Base(s.LockPath()), legacyLockFile)
	}
}
