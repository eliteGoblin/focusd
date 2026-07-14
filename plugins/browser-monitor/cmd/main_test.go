package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/plugins/browser-monitor/internal/guard"
)

// withStdin swaps os.Stdin for a pipe carrying data for the duration of
// fn, then restores it. Exercises the disguised (stdin) config path.
func withStdin(t *testing.T, data string, fn func()) {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = w.Write([]byte(data)); _ = w.Close() }()
	os.Stdin = r
	defer func() { os.Stdin = old; _ = r.Close() }()
	fn()
}

func TestReportOK(t *testing.T) {
	g := guard.New(nil,
		func() ([]guard.Tab, error) { return []guard.Tab{{App: "Safari", URL: "https://apple.com"}}, nil },
		func(string) error { return nil })
	if code := report(g); code != 0 {
		t.Errorf("clean scan => exit %d, want 0", code)
	}
}

func TestReportControlledFailure(t *testing.T) {
	g := guard.New([]string{"alibaba.com"},
		func() ([]guard.Tab, error) { return []guard.Tab{{App: "Safari", URL: "https://alibaba.com"}}, nil },
		func(string) error { return errors.New("denied") })
	if code := report(g); code != 1 {
		t.Errorf("kill failure => exit %d, want 1", code)
	}
}

func TestReportRuntimeError(t *testing.T) {
	g := guard.New(nil,
		func() ([]guard.Tab, error) { return nil, errors.New("osascript denied") },
		func(string) error { return nil })
	if code := report(g); code != 2 {
		t.Errorf("scan error => exit %d, want 2", code)
	}
}

func TestReportBlockedAndKilled(t *testing.T) {
	killed := false
	g := guard.New([]string{"youtube.com"},
		func() ([]guard.Tab, error) {
			return []guard.Tab{{App: "Safari", URL: "https://www.youtube.com/feed"}}, nil
		},
		func(string) error { killed = true; return nil })
	if code := report(g); code != 0 {
		t.Errorf("successful block+kill => exit %d, want 0", code)
	}
	if !killed {
		t.Error("expected the browser to be killed")
	}
}

// --- loadBlocklist (bytes) ---
//
// browser-monitor's run() invokes osascript (real side effects: it may
// kill live browsers), so we unit-test the refactored bytes helper and
// the stdin drain directly rather than driving full run() through stdin.

func TestLoadBlocklistDefaultWhenEmpty(t *testing.T) {
	bl, err := loadBlocklist(nil)
	if err != nil || bl != nil {
		t.Errorf("nil raw => nil,nil; got %v,%v", bl, err)
	}
}

func TestLoadBlocklistOverrideAndExtra(t *testing.T) {
	bl, err := loadBlocklist([]byte(`{"config":{"blocklist":["a.com","b.com"],"extra_blocklist":["c.com"]}}`))
	if err != nil {
		t.Fatalf("loadBlocklist: %v", err)
	}
	if len(bl) != 3 || bl[0] != "a.com" || bl[2] != "c.com" {
		t.Errorf("override+extra wrong: %v", bl)
	}
}

func TestLoadBlocklistExtraOnlyAppendsToDefault(t *testing.T) {
	bl, err := loadBlocklist([]byte(`{"config":{"extra_blocklist":["mysite.com"]}}`))
	if err != nil {
		t.Fatalf("loadBlocklist: %v", err)
	}
	if len(bl) != len(guard.DefaultBlocklist)+1 {
		t.Errorf("extra-only should be default+1, got %d", len(bl))
	}
	if bl[len(bl)-1] != "mysite.com" {
		t.Errorf("extra not appended last: %v", bl[len(bl)-1])
	}
}

func TestLoadBlocklistNoKeysUsesDefault(t *testing.T) {
	bl, err := loadBlocklist([]byte(`{"config":{}}`))
	if err != nil || bl != nil {
		t.Errorf("no keys => nil (use default); got %v,%v", bl, err)
	}
}

func TestLoadBlocklistErrors(t *testing.T) {
	if _, err := loadBlocklist([]byte(`{nope`)); err == nil {
		t.Error("expected parse error")
	}
	if _, err := loadBlocklist([]byte(`{"config":{"blocklist":"notalist"}}`)); err == nil {
		t.Error("expected type error for non-list blocklist")
	}
	if _, err := loadBlocklist([]byte(`{"config":{"extra_blocklist":[1]}}`)); err == nil {
		t.Error("expected type error for non-string entry")
	}
}

// --- readJobConfig: --config (compat) vs stdin (disguised) ---

// TestReadJobConfig_StdinMatchesFile proves a blocklist supplied via
// STDIN resolves identically to the same JSON via --config <file>.
func TestReadJobConfig_StdinMatchesFile(t *testing.T) {
	body := `{"config":{"blocklist":["a.com"],"extra_blocklist":["b.com"]}}`

	dir := t.TempDir()
	p := filepath.Join(dir, "job.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	fromFileRaw, err := readJobConfig(p)
	if err != nil {
		t.Fatalf("readJobConfig(path): %v", err)
	}
	fromFile, _ := loadBlocklist(fromFileRaw)

	var fromStdinRaw []byte
	withStdin(t, body, func() { fromStdinRaw, err = readJobConfig("") })
	if err != nil {
		t.Fatalf("readJobConfig(stdin): %v", err)
	}
	fromStdin, _ := loadBlocklist(fromStdinRaw)

	if len(fromFile) != 2 || len(fromStdin) != 2 ||
		fromFile[0] != fromStdin[0] || fromFile[1] != fromStdin[1] {
		t.Errorf("stdin %v != file %v", fromStdin, fromFile)
	}
}

func TestReadJobConfig_EmptyStdinIsNil(t *testing.T) {
	var raw []byte
	var err error
	withStdin(t, "   ", func() { raw, err = readJobConfig("") })
	if err != nil || raw != nil {
		t.Errorf("empty/whitespace stdin => nil,nil; got %v,%v", raw, err)
	}
}

func TestRunUsageAndVersion(t *testing.T) {
	if run([]string{"version"}) != 0 {
		t.Error("version should exit 0")
	}
	if run([]string{}) != 2 {
		t.Error("no args should exit 2")
	}
	if run([]string{"bogus"}) != 2 {
		t.Error("bad subcommand should exit 2")
	}
}

func TestRunErrorOnBadConfig(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{nope`), 0o644)
	if code := run([]string{"run", "--config", bad}); code != 2 {
		t.Errorf("bad config exit = %d, want 2", code)
	}
}
