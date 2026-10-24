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

func TestPathOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://www.google.com", "/"},
		{"https://www.google.com/", "/"},
		{"https://www.google.com/search?q=cats", "/search"},
		{"https://www.google.com/maps/place", "/maps/place"},
		{"https://www.google.com/?q=x", "/"},
		{"https://www.google.com?q=a/b", "/"},      // slash inside query is not the path
		{"https://www.google.com?next=/maps", "/"}, // ditto
		{"https://www.google.com/Travel/Flights#a", "/travel/flights"},
		{"www.google.com/search", "/search"}, // no scheme
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := pathOf(c.in); got != c.want {
				t.Errorf("pathOf(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestExtractHostNormalises(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://www.google.com./search", "www.google.com"}, // trailing-dot FQDN
		{"https://www.google.com#frag", "www.google.com"},    // fragment, no path
		{"https://WWW.Google.COM/search", "www.google.com"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := ExtractHost(c.in); got != c.want {
				t.Errorf("ExtractHost(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestScanGoogleSearchPathAware(t *testing.T) {
	cases := []struct {
		name string
		url  string
		kill bool
	}{
		{"search", "https://www.google.com/search?q=cats", true},
		{"webhp", "https://www.google.com/webhp", true},
		{"imghp", "https://www.google.com/imghp", true},
		{"images", "https://www.google.com/images", true},     // fail-closed: not allowlisted
		{"shopping", "https://www.google.com/shopping", true}, // fail-closed
		{"news", "https://www.google.com/news", true},
		{"homepage_slash", "https://www.google.com/", true},
		{"homepage_bare", "https://www.google.com", true},
		{"homepage_query", "https://www.google.com/?q=x", true},
		{"query_slash_homepage", "https://www.google.com?q=a/b", true}, // query-slash bug
		{"double_slash_search", "https://www.google.com//search?q=x", true},
		{"uppercase_search", "https://www.google.com/SEARCH?q=x", true},
		{"trailing_dot_search", "https://www.google.com./search?q=x", true}, // FQDN dodge
		{"au_search", "https://www.google.com.au/search?q=x", true},         // ccTLD
		{"maps", "https://www.google.com/maps", false},
		{"maps_place", "https://www.google.com/maps/place/x", false},
		{"flights_travel", "https://www.google.com/travel/flights", false},
		{"flights_short", "https://www.google.com/flights", false},
		{"redirect_url", "https://www.google.com/url?q=https://x", false},
		{"au_maps", "https://www.google.com.au/maps", false},
		{"maps_subdomain", "https://maps.google.com/", false},
		{"gmail", "https://mail.google.com/mail/u/0", false},
		{"calendar", "https://calendar.google.com/calendar", false},
		{"docs", "https://docs.google.com/document/d/x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := scanOne(t, c.url); got != c.kill {
				t.Errorf("Scan(%q): killed=%v, want %v", c.url, got, c.kill)
			}
		})
	}
}

func TestScanBlocksAdultGossipAndClassics(t *testing.T) {
	for _, u := range []string{
		"https://haijiao.com/x", "https://www.haijiao.com", "https://haijiaolove.com",
		"https://haijiaoo.com", "https://51cg.app/post/1", "https://www.51cg.app",
		"https://www.bilibili.com/video", "https://youtube.com/feed",
	} {
		t.Run(u, func(t *testing.T) {
			if !scanOne(t, u) {
				t.Errorf("Scan(%q): expected kill, got none", u)
			}
		})
	}
}

func TestIsKeywordBlocked(t *testing.T) {
	kw := []string{"haijiao"}
	block := []string{"haijiao.ai", "www.haijiao.ai", "haijiao.com", "bbs.haijiao.net",
		"haijiaoluntan.com", "haijiaoshequ.xyz", "m.haijiao.io", "HAIJIAO.AI"}
	for _, h := range block {
		if !IsKeywordBlocked(h, kw) {
			t.Errorf("IsKeywordBlocked(%q) = false, want true", h)
		}
	}
	allow := []string{"", "example.com", "youtube.com", "haiji.com"}
	for _, h := range allow {
		if IsKeywordBlocked(h, kw) {
			t.Errorf("IsKeywordBlocked(%q) = true, want false", h)
		}
	}
	if IsKeywordBlocked("haijiao.ai", nil) {
		t.Error("no keywords => never blocked")
	}
}

func TestScanKeywordKillsHaijiaoMirrors(t *testing.T) {
	// The whole haijiao forum series is caught via the keyword tier, including
	// mirrors never enumerated in the blocklist (haijiao.ai and future hops).
	for _, u := range []string{
		"https://haijiao.ai/", "https://www.haijiao.ai/post", "https://haijiaoluntan.com/x",
		"https://bbs.haijiao.net/t/1", "https://m.haijiao.io",
	} {
		t.Run(u, func(t *testing.T) {
			if !scanOne(t, u) {
				t.Errorf("Scan(%q): expected keyword kill, got none", u)
			}
		})
	}
}

// scanOne runs a single-tab Scan with the DefaultBlocklist and reports whether
// the browser was killed.
func scanOne(t *testing.T, url string) bool {
	t.Helper()
	var killed bool
	g := New(nil,
		func() ([]Tab, error) { return []Tab{{App: "Safari", URL: url}}, nil },
		func(string) error { killed = true; return nil })
	if _, err := g.Scan(); err != nil {
		t.Fatalf("Scan(%q): %v", url, err)
	}
	return killed
}
