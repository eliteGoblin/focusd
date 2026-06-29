//go:build darwin

package osadapter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// TestParsePlistReturnsArgv checks that the parsePlist walker extracts
// Label + ProgramArguments[0] AND the full argv. It uses a TEST-MODE spec
// because, post-FEATURE 14, only the test-mode plist still bakes --workdir
// into argv (prod argv is minimized to role+mesh); this keeps a meaningful
// workdirFromArgv round-trip while exercising the "--flag value" pair walk.
func TestParsePlistReturnsArgv(t *testing.T) {
	dir := t.TempDir()
	binPath := "/x/y/com.apple.metadata.helper.7f3a"
	plistPath := filepath.Join(dir, "test.plist")

	s := Spec{
		Mode:     mode.Test,
		SelfPath: binPath,
		Workdir:  "/some/hidden/wd",
		Interval: 10 * time.Second,
	}
	if err := os.WriteFile(plistPath, []byte(Plist(s, RoleA)), 0o644); err != nil {
		t.Fatal(err)
	}

	wantLabel := s.Label(RoleA)
	label, bin, argv, _ := parsePlist(plistPath)
	if label != wantLabel {
		t.Fatalf("label = %q, want %q", label, wantLabel)
	}
	if bin != binPath {
		t.Fatalf("bin = %q, want %q", bin, binPath)
	}
	if len(argv) == 0 || argv[0] != binPath {
		t.Fatalf("argv[0] = %v, want %q", argv, binPath)
	}
	if got := workdirFromArgv(argv); got != "/some/hidden/wd" {
		t.Fatalf("workdirFromArgv = %q, want /some/hidden/wd", got)
	}
}

func TestWorkdirFromArgvBothForms(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want string
	}{
		{"--workdir VAL", []string{"/bin/x", "--workdir", "/wd"}, "/wd"},
		{"--workdir=VAL", []string{"/bin/x", "--workdir=/wd"}, "/wd"},
		{"absent", []string{"/bin/x", "--github", "o/r"}, ""},
		{"dangling", []string{"/bin/x", "--workdir"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := workdirFromArgv(c.argv); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestRosterFromArgv asserts the FEATURE 10 correlation key is recovered
// from the --roster argv in both spellings, and that absence yields nil.
func TestRosterFromArgv(t *testing.T) {
	want := []string{"a.b.c", "d.e.f", "g.h.i"}
	cases := []struct {
		name string
		argv []string
		want []string
	}{
		{"--roster VAL", []string{"/bin/x", "--roster", "a.b.c,d.e.f,g.h.i"}, want},
		{"--roster=VAL", []string{"/bin/x", "--roster=a.b.c,d.e.f,g.h.i"}, want},
		{"absent", []string{"/bin/x", "--github", "o/r"}, nil},
		{"dangling", []string{"/bin/x", "--roster"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rosterFromArgv(c.argv)
			if !sameRoster(got, c.want) {
				t.Fatalf("rosterFromArgv = %v, want %v", got, c.want)
			}
		})
	}
}

// TestSameRoster covers the agreement predicate that ties three plists to
// one install (different order or content ⇒ different mesh).
func TestSameRoster(t *testing.T) {
	a := []string{"x", "y", "z"}
	if !sameRoster(a, []string{"x", "y", "z"}) {
		t.Fatal("identical rosters must match")
	}
	if sameRoster(a, []string{"x", "z", "y"}) {
		t.Fatal("reordered roster must NOT match")
	}
	if sameRoster(a, []string{"x", "y"}) {
		t.Fatal("different-length roster must NOT match")
	}
}

// TestFindCurrentInstallHappyPath writes the 3 NEW (FEATURE 14, minimized
// argv) mesh plists into a per-test "launch dir" (overridden via HOME),
// stubs the verifier to accept the install's binary, and asserts
// FindCurrentInstall recovers {Roster, Workdir, BinaryPath, 3 Plists/Labels}.
//
// FEATURE 14 reality: the disguised binary is relocated INSIDE the workdir,
// so the binary's parent dir IS the workdir, and the roster lives in the
// masked workdir file (NOT argv). The fixture mirrors that.
func TestFindCurrentInstallHappyPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	laDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(laDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The binary lives INSIDE the workdir (as relocate.RelocateInto does),
	// so Dir(bin) == workdir is the FEATURE 14 workdir-recovery path.
	wd := filepath.Join(home, "Library", "Application Support", ".com.apple.metadata.7f3a")
	binPath := filepath.Join(wd, "com.apple.metadata.helper.7f3a")
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("FAKE-DAEMON"), 0o755); err != nil {
		t.Fatal(err)
	}

	roster := []string{
		"com.apple.metadata.helper.7f3a2c11ab",
		"com.google.keystone.daemon.8c1f4e9d22",
		"org.mozilla.updater.agent.0a1b2c3d4e",
	}
	// The masked workdir roster file is the single source of truth (F14).
	if err := core.WriteRoster((&core.Store{Dir: wd}).RosterPath(), roster); err != nil {
		t.Fatal(err)
	}
	s := Spec{
		Mode: mode.User, SelfPath: binPath, Workdir: wd,
		Github: "o/r", Asset: "daemon-darwin-arm64",
		Interval: 10 * time.Second, Roster: roster,
	}
	for _, r := range AllRoles {
		pp := filepath.Join(laDir, s.Label(r)+".plist")
		if err := os.WriteFile(pp, []byte(Plist(s, r)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Add an unrelated plist that should be ignored (different binary,
	// not signed by the focusd key — but our stubbed verifier accepts
	// only the install's binary).
	other := filepath.Join(laDir, "com.unrelated.thing.plist")
	if err := os.WriteFile(other, []byte("<plist></plist>"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pass a verifier that accepts ONLY the install's binary. The
	// signature is the seam (no package-global = no data race).
	verify := Verifier(func(p string) (bool, error) { return p == binPath, nil })

	cur, err := FindCurrentInstall(mode.User, verify)
	if err != nil {
		t.Fatalf("FindCurrentInstall: %v", err)
	}
	if cur.BinaryPath != binPath {
		t.Errorf("BinaryPath = %q, want %q", cur.BinaryPath, binPath)
	}
	if !sameRoster(cur.Roster, roster) {
		t.Errorf("Roster = %v, want %v", cur.Roster, roster)
	}
	if cur.Workdir != wd {
		t.Errorf("Workdir = %q, want %q", cur.Workdir, wd)
	}
	if len(cur.PlistPaths) != 3 || len(cur.Labels) != 3 {
		t.Fatalf("want 3 plists+labels, got %d/%d", len(cur.PlistPaths), len(cur.Labels))
	}
	// Aligned: every label's plist path actually exists.
	for i, pp := range cur.PlistPaths {
		if !strings.HasSuffix(pp, cur.Labels[i]+".plist") {
			t.Errorf("plist[%d]=%q does not match label %q", i, pp, cur.Labels[i])
		}
	}
}

// oldStylePlist renders a pre-FEATURE-14 plist whose ProgramArguments bake
// --workdir/--github/--asset/--interval/--roster into argv (the leak F14
// removed). Used to prove FindCurrentInstall still discovers + correlates an
// OLD on-disk install on the NEW binary (backward-compat / migration safety).
func oldStylePlist(label, bin, wd, roster string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
  <key>Label</key><string>` + label + `</string>
  <key>ProgramArguments</key><array>
    <string>` + bin + `</string>
    <string>run</string>
    <string>--r</string>
    <string>a</string>
    <string>--mesh</string>
    <string>--workdir</string>
    <string>` + wd + `</string>
    <string>--github</string>
    <string>o/r</string>
    <string>--asset</string>
    <string>daemon-darwin-arm64</string>
    <string>--interval</string>
    <string>2s</string>
    <string>--roster</string>
    <string>` + roster + `</string>
  </array>
  <key>RunAtLoad</key><true/>
</dict></plist>
`
}

// meshFixture sets up HOME + a hidden workdir with the daemon binary inside
// it (mirrors relocate.RelocateInto) and returns {home, laDir, wd, binPath}.
func meshFixture(t *testing.T) (home, laDir, wd, binPath string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	laDir = filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(laDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wd = filepath.Join(home, "Library", "Application Support", ".com.apple.metadata.7f3a")
	binPath = filepath.Join(wd, "com.apple.metadata.helper.7f3a")
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("FAKE-DAEMON"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, laDir, wd, binPath
}

var migrationRoster = []string{
	"com.apple.metadata.helper.7f3a2c11ab",
	"com.google.keystone.daemon.8c1f4e9d22",
	"org.mozilla.updater.agent.0a1b2c3d4e",
}

// TestFindCurrentInstallOldPlists proves migration safety: an install whose
// three plists are all OLD-STYLE (roster + workdir baked in argv, NO masked
// file) is still discovered + correlated by the NEW binary. Workdir falls
// back to Dir(bin) (== wd here), roster falls back to the --roster argv.
func TestFindCurrentInstallOldPlists(t *testing.T) {
	_, laDir, wd, binPath := meshFixture(t)
	rosterCSV := strings.Join(migrationRoster, ",")
	// NO masked roster file written → forces the argv fallback.
	for _, lbl := range migrationRoster {
		pp := filepath.Join(laDir, lbl+".plist")
		if err := os.WriteFile(pp, []byte(oldStylePlist(lbl, binPath, wd, rosterCSV)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	verify := Verifier(func(p string) (bool, error) { return p == binPath, nil })
	cur, err := FindCurrentInstall(mode.User, verify)
	if err != nil {
		t.Fatalf("FindCurrentInstall: %v", err)
	}
	if cur.BinaryPath != binPath {
		t.Errorf("BinaryPath = %q, want %q", cur.BinaryPath, binPath)
	}
	if cur.Workdir != wd {
		t.Errorf("Workdir = %q, want %q (Dir(bin))", cur.Workdir, wd)
	}
	if !sameRoster(cur.Roster, migrationRoster) {
		t.Errorf("Roster = %v, want %v (from --roster argv fallback)", cur.Roster, migrationRoster)
	}
	if len(cur.PlistPaths) != 3 {
		t.Fatalf("want 3 plists, got %d", len(cur.PlistPaths))
	}
}

// TestFindCurrentInstallMixedPlists proves a HALF-MIGRATED install (some
// plists NEW/minimized, some OLD) is still discovered + correlated. The new
// correlation key is the shared verified binary path, which all plists share
// regardless of their argv shape; workdir+roster recover off the masked file.
func TestFindCurrentInstallMixedPlists(t *testing.T) {
	_, laDir, wd, binPath := meshFixture(t)
	// Masked file present (the new source of truth).
	if err := core.WriteRoster((&core.Store{Dir: wd}).RosterPath(), migrationRoster); err != nil {
		t.Fatal(err)
	}
	rosterCSV := strings.Join(migrationRoster, ",")
	s := Spec{Mode: mode.User, SelfPath: binPath, Workdir: wd, Roster: migrationRoster}

	// RoleA + RoleB → NEW minimized plists; RoleEnsure → OLD-style plist.
	newPlistA := filepath.Join(laDir, s.Label(RoleA)+".plist")
	if err := os.WriteFile(newPlistA, []byte(Plist(s, RoleA)), 0o644); err != nil {
		t.Fatal(err)
	}
	newPlistB := filepath.Join(laDir, s.Label(RoleB)+".plist")
	if err := os.WriteFile(newPlistB, []byte(Plist(s, RoleB)), 0o644); err != nil {
		t.Fatal(err)
	}
	oldEnsure := filepath.Join(laDir, s.Label(RoleEnsure)+".plist")
	if err := os.WriteFile(oldEnsure, []byte(oldStylePlist(s.Label(RoleEnsure), binPath, wd, rosterCSV)), 0o644); err != nil {
		t.Fatal(err)
	}

	verify := Verifier(func(p string) (bool, error) { return p == binPath, nil })
	cur, err := FindCurrentInstall(mode.User, verify)
	if err != nil {
		t.Fatalf("FindCurrentInstall: %v", err)
	}
	if cur.BinaryPath != binPath {
		t.Errorf("BinaryPath = %q, want %q", cur.BinaryPath, binPath)
	}
	if cur.Workdir != wd {
		t.Errorf("Workdir = %q, want %q", cur.Workdir, wd)
	}
	if !sameRoster(cur.Roster, migrationRoster) {
		t.Errorf("Roster = %v, want %v", cur.Roster, migrationRoster)
	}
	if len(cur.PlistPaths) != 3 || len(cur.Labels) != 3 {
		t.Fatalf("want 3 plists+labels across mixed install, got %d/%d", len(cur.PlistPaths), len(cur.Labels))
	}
}

// TestFindCurrentInstallMixedPlists_NoMaskedFile is the REAL transitional
// state: a crash between self-update writing the new minimized plists and
// writing the masked .roster file. Two plists are NEW (minimized argv, no
// --roster), one is OLD (carries --roster in argv), and NO masked file exists
// on disk. FindCurrentInstall must still correlate the install by the shared
// verified binary path AND recover the roster by scanning ALL matched plists'
// argv for a --roster fallback.
//
// CRITICAL fixture detail: the OLD (roster-carrying) plist is RoleA, which
// sorts FIRST in ReadDir order; the rosterless RoleEnsure sorts LAST and so
// becomes the loop's lastArgv. This is exactly the scan order that a naive
// lastArgv-only recoverRoster would mishandle (last plist has no --roster →
// nil roster). It pins the regression the all-argv scan fixes.
func TestFindCurrentInstallMixedPlists_NoMaskedFile(t *testing.T) {
	_, laDir, wd, binPath := meshFixture(t)
	// Deliberately NO masked roster file → forces the argv fallback.
	rosterCSV := strings.Join(migrationRoster, ",")
	s := Spec{Mode: mode.User, SelfPath: binPath, Workdir: wd, Roster: migrationRoster}

	// RoleA → OLD-style plist carrying --roster in argv. RoleA's dev-fallback
	// label sorts FIRST, so this roster-bearing plist is NOT the last scanned.
	oldA := filepath.Join(laDir, s.Label(RoleA)+".plist")
	if err := os.WriteFile(oldA, []byte(oldStylePlist(s.Label(RoleA), binPath, wd, rosterCSV)), 0o644); err != nil {
		t.Fatal(err)
	}
	// RoleB + RoleEnsure → NEW minimized plists (no --roster in argv). RoleEnsure
	// sorts LAST → it is the loop's lastArgv, and it carries NO roster.
	newPlistB := filepath.Join(laDir, s.Label(RoleB)+".plist")
	if err := os.WriteFile(newPlistB, []byte(Plist(s, RoleB)), 0o644); err != nil {
		t.Fatal(err)
	}
	newEnsure := filepath.Join(laDir, s.Label(RoleEnsure)+".plist")
	if err := os.WriteFile(newEnsure, []byte(Plist(s, RoleEnsure)), 0o644); err != nil {
		t.Fatal(err)
	}

	verify := Verifier(func(p string) (bool, error) { return p == binPath, nil })
	cur, err := FindCurrentInstall(mode.User, verify)
	if err != nil {
		t.Fatalf("FindCurrentInstall: %v", err)
	}
	if cur.BinaryPath != binPath {
		t.Errorf("BinaryPath = %q, want %q", cur.BinaryPath, binPath)
	}
	if cur.Workdir != wd {
		t.Errorf("Workdir = %q, want %q (Dir(bin))", cur.Workdir, wd)
	}
	// The whole point: roster recovered from the OLD plist's --roster argv,
	// even though no masked file exists and a NEW (rosterless) plist may be
	// the last one scanned.
	if !sameRoster(cur.Roster, migrationRoster) {
		t.Errorf("Roster = %v, want %v (from --roster argv fallback across all plists)", cur.Roster, migrationRoster)
	}
	if len(cur.PlistPaths) != 3 || len(cur.Labels) != 3 {
		t.Fatalf("want 3 plists+labels, got %d/%d", len(cur.PlistPaths), len(cur.Labels))
	}
}

func TestFindCurrentInstallNoneInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// LaunchAgents directory doesn't exist → zero install, nil error.
	reject := Verifier(func(string) (bool, error) { return false, nil })
	cur, err := FindCurrentInstall(mode.User, reject)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if cur.BinaryPath != "" || len(cur.PlistPaths) != 0 {
		t.Fatalf("expected empty CurInstall, got %+v", cur)
	}
}

// Go-reviewer MEDIUM #6: a broken install (plist references a binary
// that the verifier rejects — e.g. the binary was deleted or replaced
// with a non-focusd binary) is silently skipped, not returned as part
// of the install. Without this test, a future refactor could turn the
// silent-skip into a false-positive without anything catching it.
func TestFindCurrentInstallVerifyFailsSkipsPlist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	laDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(laDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(home, "hidden", "com.apple.metadata.helper.7f3a")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("FAKE-DAEMON"), 0o755); err != nil {
		t.Fatal(err)
	}
	wd := filepath.Join(home, "Library", "Application Support", ".com.apple.metadata.7f3a")
	s := Spec{
		Mode: mode.User, SelfPath: binPath, Workdir: wd,
		Github: "o/r", Asset: "daemon-darwin-arm64",
		Interval: 10 * time.Second,
		Roster: []string{
			"com.apple.metadata.helper.7f3a2c11ab",
			"com.google.keystone.daemon.8c1f4e9d22",
			"org.mozilla.updater.agent.0a1b2c3d4e",
		},
	}
	for _, r := range AllRoles {
		pp := filepath.Join(laDir, s.Label(r)+".plist")
		if err := os.WriteFile(pp, []byte(Plist(s, r)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Verifier rejects every binary (simulates binary deleted /
	// replaced / unsigned). All 3 plists must be skipped → zero install.
	rejectAll := Verifier(func(string) (bool, error) { return false, nil })
	cur, err := FindCurrentInstall(mode.User, rejectAll)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if cur.BinaryPath != "" || len(cur.PlistPaths) != 0 {
		t.Fatalf("expected zero install when verifier rejects, got %+v", cur)
	}

	// And: a verifier that returns an ERROR (vs just false) must
	// also skip — not be treated as "valid".
	rejectWithErr := Verifier(func(string) (bool, error) {
		return false, errors.New("verifier blew up")
	})
	cur, err = FindCurrentInstall(mode.User, rejectWithErr)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if cur.BinaryPath != "" || len(cur.PlistPaths) != 0 {
		t.Fatalf("expected zero install when verifier errs, got %+v", cur)
	}
}
