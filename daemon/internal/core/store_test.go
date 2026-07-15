package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRoundtrips(t *testing.T) {
	s := &Store{Dir: t.TempDir()}

	if s.HaveConfig() || s.Desired() != "" || s.Good() != "" {
		t.Fatal("fresh store should be empty")
	}
	if err := s.WriteDesired("v1"); err != nil {
		t.Fatal(err)
	}
	if !s.HaveConfig() || s.Desired() != "v1" {
		t.Fatalf("desired roundtrip failed: %q", s.Desired())
	}
	if err := s.WriteGood("v1"); err != nil {
		t.Fatal(err)
	}
	if s.Good() != "v1" {
		t.Fatalf("good roundtrip failed: %q", s.Good())
	}
	if s.BadSet()["v2"] {
		t.Fatal("v2 should not be bad yet")
	}
	if err := s.MarkBad("v2"); err != nil {
		t.Fatal(err)
	}
	if !s.BadSet()["v2"] {
		t.Fatal("v2 should be bad after MarkBad")
	}
}

func TestStoreBinPath(t *testing.T) {
	s := &Store{Dir: "/wd"}
	if got := s.BinPath("v3"); got != filepath.Join("/wd", "bin", "v3", "platform") {
		t.Fatalf("BinPath = %q", got)
	}
	if s.HaveBin("v3") {
		t.Fatal("no bin should exist")
	}
}

// TestStorePlatformDirRoutesBinPath pins FEATURE 21 (HF1): with a distinct
// PlatformDir, the platform BINARY resolves under the disposable platform-workdir
// while the daemon's own STATE (version.json/good) stays under the daemon-home
// (Dir) — i.e. daemon-state and platform-state resolve to DIFFERENT roots.
func TestStorePlatformDirRoutesBinPath(t *testing.T) {
	daemonHome := t.TempDir()
	platformWorkdir := t.TempDir()
	s := &Store{Dir: daemonHome, PlatformDir: platformWorkdir}

	// Platform binary lives under the platform-workdir, NOT the daemon-home.
	bin := s.BinPath("v3")
	if want := filepath.Join(platformWorkdir, "bin", "v3", "platform"); bin != want {
		t.Fatalf("BinPath = %q, want under platform-workdir %q", bin, want)
	}
	if strings.HasPrefix(bin, daemonHome+string(filepath.Separator)) {
		t.Fatalf("platform binary %q must NOT live under the daemon-home %q", bin, daemonHome)
	}

	// Daemon-owned state (version.json / good) lives under the daemon-home.
	if err := s.WriteDesired("v3"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(daemonHome, VersionFile)); err != nil {
		t.Fatalf("version.json must live under the daemon-home: %v", err)
	}
	if s.Desired() != "v3" {
		t.Fatalf("desired roundtrip under daemon-home failed: %q", s.Desired())
	}

	// Empty PlatformDir keeps the legacy single-root layout (BinPath under Dir).
	legacy := &Store{Dir: daemonHome}
	if got, want := legacy.BinPath("v3"), filepath.Join(daemonHome, "bin", "v3", "platform"); got != want {
		t.Fatalf("legacy BinPath = %q, want %q", got, want)
	}
}

func TestStoreSafeVersionStaysUnderBadDir(t *testing.T) {
	bad := "/store/bad"
	for _, v := range []string{"../../etc/passwd", "a/b", "x..y", "v 1.0", "ok"} {
		joined := filepath.Clean(filepath.Join(bad, safe(v)))
		if !strings.HasPrefix(joined+string(filepath.Separator), bad+string(filepath.Separator)) &&
			joined != bad {
			t.Fatalf("safe(%q) escapes bad dir: %s", v, joined)
		}
		if strings.Contains(safe(v), "..") || strings.ContainsRune(safe(v), filepath.Separator) {
			t.Fatalf("safe(%q)=%q still path-dangerous", v, safe(v))
		}
	}
}

func TestMarkBadRoundtripsSanitisedVersion(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	// A version containing a sanitised char must still be recognised
	// by its ORIGINAL string (the bug Copilot flagged).
	if err := s.MarkBad("v 1.0/beta"); err != nil {
		t.Fatal(err)
	}
	if !s.BadSet()["v 1.0/beta"] {
		t.Fatalf("bad lookup must match original version, got %v", s.BadSet())
	}
}

// WorkdirIntact is the GAP-1 wipe detector: it requires BOTH the workdir dir
// and the platform's state.db to be present. A missing dir (rm -rf) or a
// missing/absent state.db reads as broken.
func TestStoreWorkdirIntact(t *testing.T) {
	// Missing workdir dir → not intact.
	missing := &Store{Dir: filepath.Join(t.TempDir(), "gone")}
	if missing.WorkdirIntact() {
		t.Fatal("absent workdir must not read as intact")
	}

	// Dir present but no state.db → not intact.
	s := &Store{Dir: t.TempDir()}
	if s.WorkdirIntact() {
		t.Fatal("workdir with no state.db must not read as intact")
	}

	// Dir + state.db present → intact.
	if err := os.WriteFile(filepath.Join(s.Dir, PlatformStateDBName), []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !s.WorkdirIntact() {
		t.Fatal("workdir with state.db present must read as intact")
	}

	// state.db as a directory (not a file) → not intact.
	s2 := &Store{Dir: t.TempDir()}
	if err := os.Mkdir(filepath.Join(s2.Dir, PlatformStateDBName), 0o755); err != nil {
		t.Fatal(err)
	}
	if s2.WorkdirIntact() {
		t.Fatal("state.db that is a directory must not read as intact")
	}
}

// TestStoreWorkdirIntactHonorsPlatformDir is the v0.19.0 regression guard.
// FEATURE 21 (HF1) split the daemon-home (Dir) from the disposable
// platform-workdir (PlatformDir); the platform writes state.db under
// PlatformDir, not Dir. WorkdirIntact MUST therefore key off the platform-workdir
// — otherwise it stats a state.db under the daemon-home that never exists in a
// split install, always reads "wiped", and the proactive workdir-wipe heal
// restart-loops a HEALTHY platform (it never survives to be promoted to good:
// the exact `platform process down / good=none` regression).
func TestStoreWorkdirIntactHonorsPlatformDir(t *testing.T) {
	daemonHome := t.TempDir()
	platformWorkdir := t.TempDir()
	s := &Store{Dir: daemonHome, PlatformDir: platformWorkdir}

	// state.db present in the PLATFORM-workdir (where the platform actually
	// writes it) → intact — even though the daemon-home has NO state.db.
	if err := os.WriteFile(filepath.Join(platformWorkdir, PlatformStateDBName), []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !s.WorkdirIntact() {
		t.Fatal("split install: state.db in the platform-workdir must read as intact")
	}

	// Guard against a regression to the daemon-home: a state.db that exists ONLY
	// in the daemon-home (never in the platform-workdir) must NOT read as intact.
	dhOnly := &Store{Dir: t.TempDir(), PlatformDir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(dhOnly.Dir, PlatformStateDBName), []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dhOnly.WorkdirIntact() {
		t.Fatal("split install: state.db only in the daemon-home must NOT read as intact")
	}

	// A wiped platform-workdir (dir gone) reads as broken even though the
	// daemon-home still exists (the daemon runs from it).
	wiped := &Store{Dir: t.TempDir(), PlatformDir: filepath.Join(t.TempDir(), "gone")}
	if wiped.WorkdirIntact() {
		t.Fatal("split install: absent platform-workdir must not read as intact")
	}
}

func TestAtomicWriteCreatesDirs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "b", "f")
	if err := atomicWrite(p, []byte("x")); err != nil {
		t.Fatalf("atomicWrite: %v", err)
	}
	s := &Store{Dir: t.TempDir()}
	if err := s.WriteGood("v9"); err != nil || s.Good() != "v9" {
		t.Fatalf("write good through nested dirs failed: %v", err)
	}
}
