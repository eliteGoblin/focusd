package guard

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExtractHost(t *testing.T) {
	cases := map[string]string{
		"https://www.youtube.com/watch?v=x":    "www.youtube.com",
		"http://Google.COM/search?q=1":         "google.com",
		"https://user:pass@news.com.au:8443/a": "news.com.au",
		"steampowered.com/app/570":             "steampowered.com",
		"https://sub.zhihu.com":                "sub.zhihu.com",
		"":                                     "",
		"about:blank":                          "about", // scheme-less, ':' cut
		"https://alibaba.com":                  "alibaba.com",
	}
	for in, want := range cases {
		if got := ExtractHost(in); got != want {
			t.Errorf("ExtractHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsBlocked(t *testing.T) {
	bl := []string{"youtube.com", "alibaba.com", "google.com"}
	block := []string{"youtube.com", "www.youtube.com", "m.youtube.com", "alibaba.com", "deep.sub.alibaba.com"}
	for _, h := range block {
		if !IsBlocked(h, bl) {
			t.Errorf("IsBlocked(%q) = false, want true", h)
		}
	}
	allow := []string{"", "notyoutube.com", "youtube.com.evil.com", "myalibaba.com", "example.com"}
	for _, h := range allow {
		if IsBlocked(h, bl) {
			t.Errorf("IsBlocked(%q) = true, want false", h)
		}
	}
	// "youtube.com.evil.com" must NOT match youtube.com — suffix match
	// is anchored on a dot boundary, preventing the classic bypass.
}

func TestScanKillsBlockedBrowserOnce(t *testing.T) {
	tabs := []Tab{
		{App: "Safari", URL: "https://news.com.au/story"},
		{App: "Safari", URL: "https://www.youtube.com/feed"},  // same browser, 2nd hit
		{App: "Google Chrome", URL: "https://example.com/ok"}, // allowed
		{App: "Brave Browser", URL: "https://alibaba.com/x"},
	}
	var killCalls []string
	g := New([]string{"news.com.au", "youtube.com", "alibaba.com"},
		func() ([]Tab, error) { return tabs, nil },
		func(app string) error { killCalls = append(killCalls, app); return nil })

	out, err := g.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if out.Checked != 4 {
		t.Errorf("checked = %d, want 4", out.Checked)
	}
	if len(out.Blocked) != 3 {
		t.Errorf("blocked hits = %d, want 3", len(out.Blocked))
	}
	// Safari killed once despite two blocked tabs; Brave killed once.
	if len(killCalls) != 2 {
		t.Fatalf("kill calls = %v, want 2 (Safari, Brave Browser)", killCalls)
	}
	if len(out.Killed) != 2 || out.Killed[0] != "Brave Browser" || out.Killed[1] != "Safari" {
		t.Errorf("killed = %v, want sorted [Brave Browser Safari]", out.Killed)
	}
}

func TestScanNoBlockedTabsIsClean(t *testing.T) {
	g := New(nil,
		func() ([]Tab, error) { return []Tab{{App: "Safari", URL: "https://apple.com"}}, nil },
		func(string) error { t.Fatal("kill must not be called"); return nil })
	out, err := g.Scan()
	if err != nil || len(out.Killed) != 0 || len(out.Blocked) != 0 {
		t.Errorf("expected clean outcome, got %+v err=%v", out, err)
	}
}

func TestScanKillFailureRecorded(t *testing.T) {
	g := New([]string{"alibaba.com"},
		func() ([]Tab, error) { return []Tab{{App: "Safari", URL: "https://alibaba.com"}}, nil },
		func(string) error { return errors.New("pkill denied") })
	out, err := g.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(out.Killed) != 0 || len(out.Failed) != 1 {
		t.Errorf("expected 1 failure, 0 killed; got %+v", out)
	}
}

func TestScanListErrorPropagates(t *testing.T) {
	g := New(nil, func() ([]Tab, error) { return nil, errors.New("osascript boom") }, func(string) error { return nil })
	if _, err := g.Scan(); err == nil {
		t.Error("expected error when tab listing fails")
	}
}

func TestNewUsesDefaultBlocklist(t *testing.T) {
	g := New(nil, RealListTabs, RealKill)
	if len(g.Blocklist) != len(DefaultBlocklist) {
		t.Errorf("New(nil) should use DefaultBlocklist (%d entries)", len(DefaultBlocklist))
	}
	found := false
	for _, e := range g.Blocklist {
		if e == "alibaba.com" {
			found = true
		}
	}
	if !found {
		t.Error("default blocklist should contain alibaba.com (from browser_guard)")
	}
}

func TestEmbeddedScriptIsPresent(t *testing.T) {
	if len(activeTabsScript) == 0 {
		t.Fatal("AppleScript was not embedded into the binary")
	}
	for _, must := range []string{"Safari", "Google Chrome", "URL of t", "ASCII character 9"} {
		if !contains(activeTabsScript, must) {
			t.Errorf("embedded script missing %q", must)
		}
	}
}

func TestRealListTabsIsReadOnlyAndSafe(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("osascript is macOS-only")
	}
	if os.Getenv("FOCUSD_OSA_TEST") == "" {
		t.Skip("set FOCUSD_OSA_TEST=1 to run the live osascript read (slow; reads real tabs)")
	}
	// Read-only: lists tabs, never kills. Automation permission or a
	// headless CI agent may legitimately deny this — skip, don't fail.
	tabs, err := RealListTabs()
	if err != nil {
		t.Skipf("osascript unavailable/denied (expected in CI): %v", err)
	}
	for _, tb := range tabs {
		if tb.App == "" || tb.URL == "" {
			t.Errorf("malformed tab parsed: %+v", tb)
		}
	}
}

func TestParseTabs(t *testing.T) {
	in := []byte("Safari\thttps://a.com\r\n" +
		"Google Chrome\thttps://b.com/x?y=1\n" +
		"\n" + // blank
		"BadLineNoTab\n" + // no tab -> skipped
		"Edge\t  \n" + // empty url -> skipped
		"Brave Browser\thttps://c.com\n")
	got := parseTabs(in)
	if len(got) != 3 {
		t.Fatalf("parsed %d tabs, want 3: %+v", len(got), got)
	}
	if got[0] != (Tab{"Safari", "https://a.com"}) {
		t.Errorf("tab0 = %+v", got[0])
	}
	if got[1].App != "Google Chrome" || got[1].URL != "https://b.com/x?y=1" {
		t.Errorf("tab1 = %+v", got[1])
	}
	if got[2].App != "Brave Browser" {
		t.Errorf("tab2 = %+v", got[2])
	}
}

func TestRealListTabsSurfacesOsascriptFailure(t *testing.T) {
	orig := osascriptPath
	osascriptPath = filepath.Join(t.TempDir(), "no-such-osascript")
	defer func() { osascriptPath = orig }()

	if _, err := RealListTabs(); err == nil {
		t.Error("expected error when osascript binary is missing")
	}
}

func TestParseTabsEmpty(t *testing.T) {
	if got := parseTabs([]byte("")); len(got) != 0 {
		t.Errorf("empty output => no tabs, got %+v", got)
	}
}

func TestClassifyPkillErr(t *testing.T) {
	if !classifyPkillErr(nil) {
		t.Error("nil err must be benign (process killed)")
	}
	// exec.ExitError with code 1 == "no process matched" == benign.
	cmd := execCommandExit(t, 1)
	if !classifyPkillErr(cmd) {
		t.Error("pkill exit 1 (no match) must be benign")
	}
	cmd2 := execCommandExit(t, 2)
	if classifyPkillErr(cmd2) {
		t.Error("pkill exit 2 must be a real failure")
	}
	if classifyPkillErr(errors.New("spawn failed")) {
		t.Error("non-exit error must be a real failure")
	}
}

// execCommandExit returns the *exec.ExitError produced by a process that
// exits with the given code (a real ExitError, like pkill would give).
func execCommandExit(t *testing.T, code int) error {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs POSIX sh")
	}
	err := exec.Command("/bin/sh", "-c", "exit "+itoa(code)).Run()
	if err == nil {
		t.Fatalf("expected non-nil error for exit %d", code)
	}
	return err
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
