//go:build darwin

package osadapter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// TestParsePlistReturnsArgv checks that the rewritten parsePlist
// extracts Label + ProgramArguments[0] AND the full argv (needed by
// self-update to recover --workdir from an existing install).
func TestParsePlistReturnsArgv(t *testing.T) {
	dir := t.TempDir()
	binPath := "/x/y/com.apple.metadata.helper.7f3a"
	plistPath := filepath.Join(dir, "test.plist")

	roster := []string{
		"com.apple.metadata.helper.7f3a2c11ab",
		"com.google.keystone.daemon.8c1f4e9d22",
		"org.mozilla.updater.agent.0a1b2c3d4e",
	}
	s := Spec{
		Mode:     mode.User,
		SelfPath: binPath,
		Workdir:  "/some/hidden/wd",
		Github:   "o/r",
		Asset:    "daemon-darwin-arm64",
		Interval: 10 * time.Second,
		Roster:   roster,
	}
	if err := os.WriteFile(plistPath, []byte(Plist(s, RoleA)), 0o644); err != nil {
		t.Fatal(err)
	}

	label, bin, argv := parsePlist(plistPath)
	if label != roster[0] {
		t.Fatalf("label = %q, want %q", label, roster[0])
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

// TestFindCurrentInstallHappyPath writes the 3 mesh plists into a
// per-test "launch dir" (overridden via HOME), stubs the verifier to
// accept the install's binary, and asserts FindCurrentInstall recovers
// {Base, Workdir, BinaryPath, 3 PlistPaths/Labels}.
func TestFindCurrentInstallHappyPath(t *testing.T) {
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
	roster := []string{
		"com.apple.metadata.helper.7f3a2c11ab",
		"com.google.keystone.daemon.8c1f4e9d22",
		"org.mozilla.updater.agent.0a1b2c3d4e",
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
