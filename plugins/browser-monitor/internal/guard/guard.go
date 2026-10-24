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
	// Search — www.google.com only, and (via Scan's path-aware carve-out)
	// its SEARCH surfaces only. Maps/Flights/Travel on the same host, and
	// the other Google properties on their own subdomains (mail, calendar,
	// drive, docs, meet, accounts, maps.google.com), are deliberately NOT
	// matched here so they keep working. The apex google.com is handled at
	// the DNS layer, not here, because a host-only match can't tell
	// www.google.com/search from www.google.com/maps. The AU ccTLD is
	// included because Google redirects AU users to it.
	"www.google.com", "www.google.com.au",
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
	// Adult / gossip (always-block; www. and other subdomains match via IsBlocked)
	"haijiao.com", "haijiaolove.com", "haijiaoo.com", "51cg.app",
}

// DefaultKeywords is the substring-match tier: a tab whose host CONTAINS any of
// these is killed, regardless of TLD or subdomain. It exists for targets that
// domain-hop across mirrors (the haijiao forum: haijiao.com -> haijiao.ai -> the
// next mirror next month), which a fixed host list can never keep up with.
// Matching is a plain case-insensitive substring (see IsKeywordBlocked), so keep
// entries distinctive enough not to catch legitimate hosts. The mac-browser-guard
// Python util's KEYWORDS is GENERATED from this list too (see the //go:generate).
var DefaultKeywords = []string{"haijiao"}

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
	Keywords  []string
	ListTabs  func() ([]Tab, error)
	Kill      func(app string) error
}

// New builds a Guard. Empty blocklist => DefaultBlocklist.
func New(blocklist []string, list func() ([]Tab, error), kill func(string) error) *Guard {
	if len(blocklist) == 0 {
		blocklist = DefaultBlocklist
	}
	return &Guard{Blocklist: blocklist, Keywords: DefaultKeywords, ListTabs: list, Kill: kill}
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
		if !IsBlocked(host, g.Blocklist) && !IsKeywordBlocked(host, g.Keywords) {
			continue
		}
		// Path-aware carve-out: the Google search hosts are blocklisted, but a
		// non-search tab on them (Maps, Flights, Travel, …) is left alone so the
		// browser isn't killed for using those. Everything else on the host —
		// search, images, shopping, the homepage — stays blocked (fail closed).
		if googleSearchHosts[host] && isGoogleAllowed(t.URL) {
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
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	// Strip userinfo if present (user:pass@host).
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	// Normalise a trailing-dot FQDN (www.google.com. == www.google.com) so it
	// can't dodge the blocklist.
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
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

// IsKeywordBlocked reports whether host CONTAINS any keyword as a
// case-insensitive substring. Unlike IsBlocked (exact-or-subdomain), this
// matches anywhere in the host, so a domain-hopping target is caught across
// every mirror and TLD (haijiao.ai, bbs.haijiao.net, haijiaoluntan.com, ...).
// Empty host and empty/blank keywords never match.
func IsKeywordBlocked(host string, keywords []string) bool {
	if host == "" {
		return false
	}
	host = strings.ToLower(host)
	for _, k := range keywords {
		k = strings.ToLower(strings.TrimSpace(k))
		if k != "" && strings.Contains(host, k) {
			return true
		}
	}
	return false
}

// googleSearchHosts are the Google hosts given the path-aware carve-out: their
// search surfaces are blocked while their non-search surfaces (Maps/Flights/
// Travel) stay allowed. The AU ccTLD is included because Google redirects AU
// users to it. Gmail/Calendar/Drive live on their own subdomains (mail.,
// calendar., drive.google.com) and are never matched here at all.
var googleSearchHosts = map[string]bool{
	"www.google.com":    true,
	"www.google.com.au": true,
}

// googleAllowedPrefixes is the ONLY set of path prefixes on a googleSearchHost
// that are treated as non-search and left alone. The match is an ALLOWLIST so
// it FAILS CLOSED: any path not listed here — /search, /images, /shopping,
// /news, /finance, the bare homepage, odd shapes like //search or percent-
// encoded paths — is treated as search and blocked. That's the right default
// for a commitment tool: a surface we forgot errs toward blocking, not leaking.
var googleAllowedPrefixes = []string{"/maps", "/travel", "/flights", "/url", "/recaptcha", "/sorry"}

// isGoogleAllowed reports whether a googleSearchHost URL points at a non-search
// surface that must keep working. Fails closed (see googleAllowedPrefixes).
func isGoogleAllowed(rawURL string) bool {
	p := pathOf(rawURL)
	for _, pre := range googleAllowedPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	return false
}

// pathOf returns the lowercased path of rawURL (leading "/..."), without query
// or fragment. Query and fragment are stripped BEFORE the path is located so a
// '/' inside them isn't mistaken for the path. A URL with a host but no path
// yields "/". Deliberately lenient: never errors, mirrors ExtractHost's parsing.
func pathOf(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	i := strings.IndexByte(s, '/')
	if i < 0 {
		return "/" // host only, no path → homepage
	}
	return strings.ToLower(s[i:])
}
