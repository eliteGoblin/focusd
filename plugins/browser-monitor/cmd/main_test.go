package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/plugins/browser-monitor/internal/guard"
)

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

func TestLoadBlocklistDefaultWhenNoPath(t *testing.T) {
	bl, err := loadBlocklist("")
	if err != nil || bl != nil {
		t.Errorf("no path => nil,nil; got %v,%v", bl, err)
	}
}

func TestLoadBlocklistOverrideAndExtra(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "job.json")
	os.WriteFile(p, []byte(`{"config":{"blocklist":["a.com","b.com"],"extra_blocklist":["c.com"]}}`), 0o644)
	bl, err := loadBlocklist(p)
	if err != nil {
		t.Fatalf("loadBlocklist: %v", err)
	}
	if len(bl) != 3 || bl[0] != "a.com" || bl[2] != "c.com" {
		t.Errorf("override+extra wrong: %v", bl)
	}
}

func TestLoadBlocklistExtraOnlyAppendsToDefault(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "job.json")
	os.WriteFile(p, []byte(`{"config":{"extra_blocklist":["mysite.com"]}}`), 0o644)
	bl, err := loadBlocklist(p)
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
	dir := t.TempDir()
	p := filepath.Join(dir, "job.json")
	os.WriteFile(p, []byte(`{"config":{}}`), 0o644)
	bl, err := loadBlocklist(p)
	if err != nil || bl != nil {
		t.Errorf("no keys => nil (use default); got %v,%v", bl, err)
	}
}

func TestLoadBlocklistErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{nope`), 0o644)
	if _, err := loadBlocklist(bad); err == nil {
		t.Error("expected parse error")
	}
	if _, err := loadBlocklist(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("expected read error")
	}
	wt := filepath.Join(dir, "wt.json")
	os.WriteFile(wt, []byte(`{"config":{"blocklist":"notalist"}}`), 0o644)
	if _, err := loadBlocklist(wt); err == nil {
		t.Error("expected type error for non-list blocklist")
	}
	we := filepath.Join(dir, "we.json")
	os.WriteFile(we, []byte(`{"config":{"extra_blocklist":[1]}}`), 0o644)
	if _, err := loadBlocklist(we); err == nil {
		t.Error("expected type error for non-string entry")
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
