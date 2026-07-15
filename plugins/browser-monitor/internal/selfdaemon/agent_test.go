package selfdaemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestAgent returns an Agent rooted entirely in dir with fake launchctl/cron
// seams, so the full lifecycle runs without touching the real launchd or cron.
func newTestAgent(t *testing.T, dir string) (*Agent, *fakeSystem) {
	t.Helper()
	fs := &fakeSystem{}
	return &Agent{
		Copies:    []string{filepath.Join(dir, "copyA"), filepath.Join(dir, "sub", "copyB")},
		PlistPath: filepath.Join(dir, "agent.plist"),
		Label:     "com.example.test",
		CronTag:   "# com.example.test",
		LogPath:   filepath.Join(dir, "log"),
		Interval:  10,

		ReadExecutable: func() ([]byte, error) { return []byte("GENUINE-BINARY"), nil },
		Launchctl:      fs.launchctl,
		ReadCrontab:    fs.readCrontab,
		WriteCrontab:   fs.writeCrontab,
		Scan:           func() int { fs.scans++; return 0 },
	}, fs
}

type fakeSystem struct {
	cron        string
	launchCalls []string
	scans       int
}

func (f *fakeSystem) launchctl(args ...string) error {
	f.launchCalls = append(f.launchCalls, strings.Join(args, " "))
	return nil
}
func (f *fakeSystem) readCrontab() (string, error) { return f.cron, nil }
func (f *fakeSystem) writeCrontab(s string) error  { f.cron = s; return nil }

func TestInstallDeploysAllPieces(t *testing.T) {
	dir := t.TempDir()
	a, fs := newTestAgent(t, dir)

	if err := a.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, c := range a.Copies {
		assertFileIs(t, c, "GENUINE-BINARY", 0o755)
	}
	if !fileExists(a.PlistPath) {
		t.Error("plist not written")
	}
	if !strings.Contains(fs.cron, a.CronTag) {
		t.Errorf("cron line not installed: %q", fs.cron)
	}
	// A reload = at least one unload + one load.
	if !contains(fs.launchCalls, "unload "+a.PlistPath) || !contains(fs.launchCalls, "load "+a.PlistPath) {
		t.Errorf("expected unload+load, got %v", fs.launchCalls)
	}
}

func TestHealRestoresDeletedPieces(t *testing.T) {
	dir := t.TempDir()
	a, fs := newTestAgent(t, dir)
	if err := a.Install(); err != nil {
		t.Fatal(err)
	}

	// Casually delete one copy, the plist, and the cron line.
	if err := os.Remove(a.Copies[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(a.PlistPath); err != nil {
		t.Fatal(err)
	}
	fs.cron = ""

	if err := a.Heal(); err != nil {
		t.Fatalf("Heal: %v", err)
	}
	assertFileIs(t, a.Copies[0], "GENUINE-BINARY", 0o755)
	if !fileExists(a.PlistPath) {
		t.Error("Heal did not restore the plist")
	}
	if !strings.Contains(fs.cron, a.CronTag) {
		t.Error("Heal did not restore the cron line")
	}
}

func TestHealFromSurvivorWhenExecutableGone(t *testing.T) {
	dir := t.TempDir()
	a, _ := newTestAgent(t, dir)
	// Executable unreadable — must fall back to a surviving copy.
	a.ReadExecutable = func() ([]byte, error) { return nil, os.ErrNotExist }
	if err := writeFile(a.Copies[1], []byte("SURVIVOR"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := a.Heal(); err != nil {
		t.Fatalf("Heal: %v", err)
	}
	assertFileIs(t, a.Copies[0], "SURVIVOR", 0o755) // restored from the survivor copy
}

func TestUninstallRemovesEverything(t *testing.T) {
	dir := t.TempDir()
	a, fs := newTestAgent(t, dir)
	if err := a.Install(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.LogPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := a.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	for _, p := range append([]string{a.PlistPath, a.LogPath}, a.Copies...) {
		if fileExists(p) {
			t.Errorf("Uninstall left %s behind", p)
		}
	}
	if strings.Contains(fs.cron, a.CronTag) {
		t.Errorf("Uninstall left cron line: %q", fs.cron)
	}
}

func TestUninstallIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	a, _ := newTestAgent(t, dir)
	// Never installed — uninstall must not error on already-gone pieces.
	if err := a.Uninstall(); err != nil {
		t.Fatalf("Uninstall on empty state: %v", err)
	}
}

func TestTickHealsThenScans(t *testing.T) {
	dir := t.TempDir()
	a, fs := newTestAgent(t, dir)
	if err := a.Install(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(a.Copies[0]); err != nil {
		t.Fatal(err)
	}

	if code := a.Tick(); code != 0 {
		t.Errorf("Tick exit = %d, want 0", code)
	}
	if fs.scans != 1 {
		t.Errorf("Tick scanned %d times, want 1", fs.scans)
	}
	if !fileExists(a.Copies[0]) {
		t.Error("Tick did not heal the deleted copy before scanning")
	}
}

func TestPlistAndCronContent(t *testing.T) {
	dir := t.TempDir()
	a, _ := newTestAgent(t, dir)
	plist := a.plistXML()
	for _, must := range []string{a.Label, a.Copies[0], "self-tick", "<integer>10</integer>", a.LogPath} {
		if !strings.Contains(plist, must) {
			t.Errorf("plist missing %q\n%s", must, plist)
		}
	}
	cron := a.cronLine()
	// cron runs the LAST copy, not the plist's first.
	if !strings.Contains(cron, a.Copies[len(a.Copies)-1]) || !strings.Contains(cron, "self-tick") || !strings.Contains(cron, a.CronTag) {
		t.Errorf("cron line wrong: %q", cron)
	}
	if !strings.HasPrefix(cron, "*/5 * * * *") {
		t.Errorf("cron cadence not 5m: %q", cron)
	}
}

func TestXMLEscape(t *testing.T) {
	if got := xmlEscape(`a&b<c>d"e`); got != "a&amp;b&lt;c&gt;d&quot;e" {
		t.Errorf("xmlEscape wrong: %q", got)
	}
}

func assertFileIs(t *testing.T, path, want string, mode os.FileMode) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(b) != want {
		t.Errorf("%s content = %q, want %q", path, b, want)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != mode {
		t.Errorf("%s mode = %v, want %v", path, fi.Mode().Perm(), mode)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
