package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
)

// FEATURE 14 / ADR-0018: a mesh role (a `run … --mesh` worker or the
// `ensure` subcommand) launched from a NEW minimized plist carries neither
// --workdir nor --roster. parse() must recover both off-argv: the workdir
// from the daemon binary's parent dir, the roster from the masked workdir
// file. These tests lock that derivation + the old-plist backward-compat.

// execDir is the parent dir of the running test binary — what
// deriveMeshWorkdir() resolves to (filepath.Dir(os.Executable())) when no
// explicit --workdir is given.
func execDir(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return filepath.Dir(self)
}

func TestParseMeshDerivesWorkdirFromBinary(t *testing.T) {
	// A worker mesh role (`run … --mesh`) with NO --workdir must derive the
	// workdir from the binary's parent dir.
	o := parse("run", []string{"--r", "a", "--mesh"})
	if o.workdir != execDir(t) {
		t.Fatalf("mesh workdir = %q, want derived %q", o.workdir, execDir(t))
	}
}

func TestParseEnsureMeshDerivesWorkdir(t *testing.T) {
	// The `ensure` subcommand is a mesh role even without --mesh.
	o := parse("ensure", []string{})
	if o.workdir != execDir(t) {
		t.Fatalf("ensure workdir = %q, want derived %q", o.workdir, execDir(t))
	}
}

func TestParseMeshExplicitWorkdirWins(t *testing.T) {
	// An explicit --workdir (old plist, or operator) overrides derivation.
	o := parse("run", []string{"--r", "a", "--mesh", "--workdir", "/explicit/wd"})
	if o.workdir != "/explicit/wd" {
		t.Fatalf("explicit --workdir not honored: got %q", o.workdir)
	}
}

func TestParseMeshLoadsRosterFromMaskedFile(t *testing.T) {
	// New minimized plist: no --roster on argv. parse() must read the roster
	// from the masked file under the (explicit) workdir.
	wd := t.TempDir()
	want := []string{
		"com.apple.metadata.helper.7f3a2c11ab",
		"com.google.keystone.daemon.8c1f4e9d22",
		"org.mozilla.updater.agent.0a1b2c3d4e",
	}
	if err := core.WriteRoster((&core.Store{Dir: wd}).RosterPath(), want); err != nil {
		t.Fatal(err)
	}
	o := parse("run", []string{"--r", "a", "--mesh", "--workdir", wd})
	if len(o.roster) != 3 {
		t.Fatalf("roster not loaded from masked file: %v", o.roster)
	}
	for i := range want {
		if o.roster[i] != want[i] {
			t.Fatalf("roster[%d] = %q, want %q", i, o.roster[i], want[i])
		}
	}
}

func TestParseMeshExplicitRosterWins(t *testing.T) {
	// Old plist: --roster on argv. It must WIN even when a masked file is
	// present (backward-compat: the old plist's baked roster is authoritative
	// for that already-running mesh).
	wd := t.TempDir()
	if err := core.WriteRoster((&core.Store{Dir: wd}).RosterPath(),
		[]string{"file.a", "file.b", "file.c"}); err != nil {
		t.Fatal(err)
	}
	o := parse("run", []string{"--r", "a", "--mesh", "--workdir", wd, "--roster", "argv.a,argv.b,argv.c"})
	if len(o.roster) != 3 || o.roster[0] != "argv.a" || o.roster[2] != "argv.c" {
		t.Fatalf("explicit --roster must win over masked file: %v", o.roster)
	}
}

func TestParseMeshMissingRosterFileLeavesNil(t *testing.T) {
	// No --roster AND no masked file → roster stays nil (Spec.Label's dev
	// fallback applies; parse must NOT crash).
	wd := t.TempDir() // empty: no .roster file
	o := parse("run", []string{"--r", "a", "--mesh", "--workdir", wd})
	if o.roster != nil {
		t.Fatalf("missing roster file must leave roster nil, got %v", o.roster)
	}
}

func TestParseMeshShortRosterFileLeavesNil(t *testing.T) {
	// A truncated/edited masked file with fewer than the 3 mesh labels must be
	// treated as unreadable → roster stays nil (NOT a partial slice that would
	// let Spec.Label backfill missing positions with dev labels). (Copilot.)
	wd := t.TempDir()
	if err := core.WriteRoster((&core.Store{Dir: wd}).RosterPath(),
		[]string{"only.one.label"}); err != nil {
		t.Fatal(err)
	}
	o := parse("run", []string{"--r", "a", "--mesh", "--workdir", wd})
	if o.roster != nil {
		t.Fatalf("short masked roster must be rejected (nil), got %v", o.roster)
	}
}

func TestParseNonMeshDoesNotDeriveOrLoad(t *testing.T) {
	// A plain `run` (no --mesh) is NOT a mesh role: it must keep the default
	// workdir and must NOT read a masked roster file.
	o := parse("run", []string{"--r", "a"})
	if o.workdir != defaultWorkdir() {
		t.Fatalf("non-mesh run must keep default workdir, got %q", o.workdir)
	}
	if o.roster != nil {
		t.Fatalf("non-mesh run must not load a roster, got %v", o.roster)
	}
}
