// Package guard implements browser distraction protection: read the
// active tab URLs of running browsers, and if any resolves to a
// blocklisted host, kill that browser. Ported from app_mon/browser_guard
// (the bash companion shipped in PR #18) into a single Go plugin.
//
// All OS interaction (osascript, pkill) is injected via seams so tests
// never touch real browsers or processes.
package guard

import (
	"fmt"
	"sort"
	"strings"
)

// DefaultBlocklist is the SINGLE SOURCE OF TRUTH for the browser-guard
// blocklist. Hosts are matched exactly or as a parent domain (see IsBlocked).
// The mac-browser-guard Python util's BLOCKLIST is GENERATED from this list, so
// the two never drift — after editing, regenerate it:
//
//go:generate go run github.com/eliteGoblin/focusd/plugins/browser-monitor/internal/blocklistsync/gen
var DefaultBlocklist = []string{
	// Search
	"google.com",
	// Video / streaming
	"youtube.com", "bilibili.com",
	// Gaming
	"steampowered.com", "steamcommunity.com", "steamcontent.com",
	"steamstatic.com", "dota2.com", "dota.com", "chronodivide.com",
	"dos.zone", "play-cs.com", "webrcade.com",
	// News / doomscroll
	"9news.com.au", "abc.net.au", "news.com.au", "smh.com.au",
	"espn.com.au", "theaustralian.com.au", "163.com", "iranintl.com",
	"southcn.com", "tmtpost.com",
	// Misc
	"zhihu.com", "heheda.top", "alibaba.com",
}

// Tab is one open browser tab.
type Tab struct {
	App string // e.g. "Safari", "Google Chrome"
	URL string
}

// BlockedHit records why a browser was killed.
type BlockedHit struct {
	App  string `json:"app"`
	Host string `json:"host"`
	URL  string `json:"url"`
}

// Outcome summarises one protection pass.
type Outcome struct {
	Checked int          `json:"checked"`
	Blocked []BlockedHit `json:"blocked,omitempty"`
	Killed  []string     `json:"killed,omitempty"`
	Failed  []string     `json:"failed,omitempty"` // "app: reason"
}

// Guard orchestrates a scan. ListTabs and Kill are seams.
type Guard struct {
	Blocklist []string
	ListTabs  func() ([]Tab, error)
	Kill      func(app string) error
}

// New builds a Guard. Empty blocklist => DefaultBlocklist.
func New(blocklist []string, list func() ([]Tab, error), kill func(string) error) *Guard {
	if len(blocklist) == 0 {
		blocklist = DefaultBlocklist
	}
	return &Guard{Blocklist: blocklist, ListTabs: list, Kill: kill}
}

// Scan reads tabs and kills any browser showing a blocklisted host. A
// browser is killed at most once per pass (dedup), matching the bash
// guard's behaviour.
func (g *Guard) Scan() (Outcome, error) {
	tabs, err := g.ListTabs()
	if err != nil {
		return Outcome{}, fmt.Errorf("list tabs: %w", err)
	}

	var out Outcome
	out.Checked = len(tabs)
	killed := map[string]bool{}

	for _, t := range tabs {
		host := ExtractHost(t.URL)
		if !IsBlocked(host, g.Blocklist) {
			continue
		}
		out.Blocked = append(out.Blocked, BlockedHit{App: t.App, Host: host, URL: t.URL})
		if killed[t.App] {
			continue // already killed this browser this pass
		}
		killed[t.App] = true
		if err := g.Kill(t.App); err != nil {
			out.Failed = append(out.Failed, fmt.Sprintf("%s: %v", t.App, err))
			continue
		}
		out.Killed = append(out.Killed, t.App)
	}
	sort.Strings(out.Killed)
	return out, nil
}

// ExtractHost reduces a URL to its lowercase host, mirroring the bash
// guard's extract_host (strip scheme, path, query, port). Deliberately
// lenient — never errors, returns "" for empty input.
func ExtractHost(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	// Strip userinfo if present (user:pass@host).
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// IsBlocked reports whether host matches a blocklist entry exactly or as
// a subdomain (foo.example.com matches example.com). Mirrors the bash
// guard's is_blocked.
func IsBlocked(host string, blocklist []string) bool {
	if host == "" {
		return false
	}
	for _, e := range blocklist {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if host == e || strings.HasSuffix(host, "."+e) {
			return true
		}
	}
	return false
}
