# Monitor Web Browser Tab URL

## Context

The existing `app_mon` defenses cover:

- **Process layer** — kill blocked apps (Steam, Dota 2) via `FindByName` + SIGKILL.
- **Filesystem layer** — delete blocked app bundles, caches, save data.
- **DNS layer** — `/etc/hosts` blackhole for ~68 hostnames (Steam, YouTube, Bilibili, news domains, etc.).

The DNS layer is the only network-level block today, and it leaks in three real cases observed on this machine and in the wider macOS ecosystem:

1. **DNS-over-HTTPS (DoH)** — Chrome, Brave, Firefox each ship with their own DoH resolver that bypasses `/etc/hosts` entirely. A user has only to enable "Use secure DNS" in browser settings (Chrome) or check the box in Firefox network settings, and every domain in our blocklist resolves normally again.
2. **AAAA / IPv6 leak** — `/etc/hosts` entries are `0.0.0.0 <host>` (IPv4 only). Dual-stack domains like `youtube.com` still resolve via real DNS for their AAAA record. Apps that prefer IPv6 connect successfully. (Documented as deferred follow-up in v0.6.0 CHANGELOG.)
3. **Hardcoded IPs / hardcoded resolvers** — some apps (Steam's CDN, several Chinese sites) embed IPs or use private resolvers. DNS blocking doesn't reach them.

For browsers specifically, the threat is acute: the whole *point* of a browser is to visit URLs, and the URL bar is the user's path-of-least-resistance bypass. A blocked domain in `/etc/hosts` is a 2-click bypass via DoH; switching to a different DNS over HTTPS is a 1-click action in every modern browser.

This requirements doc scopes a new layer: **read browser tab URLs as the source of truth** and act on blocked URLs at the browser-tab level, downstream of any DNS bypass.

## Goal

Detect blocked URLs that have already loaded (or are loading) in any browser tab on macOS, regardless of how the browser resolved DNS, and take action: **close the tab, kill the browser, log the event**. Same self-binding / friction philosophy as `app_mon` — present-you wants this protection, future-you in a moment of weakness is the adversary.

## Scope: in / out

### In scope (MVP)

- **Browsers**: Safari, Google Chrome, Brave. These three cover ~99% of macOS usage and all expose tab URLs via AppleScript.
- **Detection mechanism**: AppleScript polling every 10–30s, asking each running browser for the URL of every tab in every window. (Same cadence as the existing `app_mon` quick-kill tick.)
- **Blocklist source**: reuse `internal/policy/dns_blocklist.go`'s `DefaultDNSBlocklist`. A URL whose host matches any entry (with simple subdomain rules) is blocked.
- **Action on match**: close the offending tab via AppleScript. Log the event with timestamp + URL + browser. If a browser has ≥3 blocked tabs in a single tick, escalate to **kill the browser process** (forces user to relaunch — friction multiplier).
- **No-stop semantics**: same as `app_mon`. No CLI command to disable browser monitoring. Requires source edit + rebuild + sudo-replace of the binary to remove.

### Out of scope (MVP)

- **Firefox**: doesn't expose tabs via AppleScript by default; would require a custom WebExtension. Add later if needed.
- **Webkit views inside other apps** (Slack message previews, Notion's web views, etc.): too many surfaces, too low signal.
- **HTTPS interception / proxy**: separate Layer 6 — see "Future" below.
- **Per-URL whitelisting**: blocklist is the only directional list. To unblock, edit `dns_blocklist.go` and rebuild (the existing perm-ban semantic).

## Functional requirements

### F1. Periodic tab scan

Every 15 seconds (configurable, but compiled-in default), the daemon must:

1. Enumerate running browsers from the set `{Safari, Google Chrome, Brave Browser}`.
2. For each running browser, run AppleScript to get the URL of every tab in every window.
3. For each URL, normalize the host (strip scheme, port, path, query) and compare against `DefaultDNSBlocklist` with the same subdomain-tolerant matching used by the `/etc/hosts` layer.
4. For each blocked URL, close the tab via AppleScript (`close tab id … of window id …`).
5. Log structured: `{ts, browser, window_id, tab_id, url, action: "tab_closed" | "browser_killed"}`.

### F2. Browser-kill escalation

When a single scan tick detects ≥3 blocked tabs across all browsers, the daemon must SIGKILL the browser process(es) where the blocked tabs live. Rationale: closing tabs one-by-one is too gentle when the user is actively re-opening; killing the process is a meaningful 5–10 second friction (relaunch, sign-in, restore session) that breaks the impulse loop.

The threshold (3) is compiled-in.

### F3. Permission acquisition

AppleScript control of Safari / Chrome / Brave requires the `appmon` binary to be granted **Automation** permission for each target browser (System Settings → Privacy & Security → Automation → appmon → Safari/Chrome/Brave). The daemon must:

- Attempt the AppleScript and log a clear warning if it fails due to permission errors.
- Print an instructional CLI message on `appmon start` if permissions are missing: which apps need toggling, how to do it.
- **Not crash or stop other protections** if browser permission is denied — user might not have granted it yet.

### F4. Privacy

URLs visited are sensitive. The daemon must:

- Never persist non-blocked URLs anywhere (memory only, dropped after the tick).
- Log only **blocked** URLs to the standard log file (`/var/tmp/appmon.log`).
- Never transmit URLs anywhere (no telemetry, no cloud upload). Future cloud-sync layer is explicitly excluded from this feature.

### F5. Integration with existing watcher

The browser monitor lives inside the existing `Watcher` daemon (not a new process). New ticker `BrowserScanInterval = 15 * time.Second`. The `EnforcementResult` shape gains a `BrowserActions []BrowserAction` field surfaced through `appmon status`.

No new daemon, no new plist, no new launchd integration — this is "Layer 6" inside the existing protection stack.

## Non-functional requirements

### Resilience

- **AppleScript failure**: log warn, continue. Don't disable other layers.
- **Browser not running**: silently skip — no error.
- **AppleScript timeout** (browser hung): bound each per-browser query at 3 seconds; on timeout, skip that browser this tick.
- **Permission revoked mid-flight**: graceful degrade with structured log warning, retry next tick.

### Performance

- Total scan should complete in ≤2 seconds per tick under normal load (a few dozen tabs).
- AppleScript invocations are batched per browser (one `osascript` call returns all tabs in all windows).
- Compiled blocklist match is O(n_tabs × n_blocklist) with simple hostname normalization. ~50 tabs × 68 entries × subdomain check is microseconds.

### Friction (matches app_mon)

- No `stop` / `disable` / `pause` CLI command.
- Disabling requires either revoking Automation permission in System Settings (loud, user-visible) **and** appmon must detect this and either restore via `osascript` re-permission prompt OR escalate (e.g. start logging the permission state as a status warning, or kill the affected browser on every scan tick since "we can't see what's open, assume the worst").
- Compiled blocklist is the same `DefaultDNSBlocklist` — single source of truth; removing a URL requires source edit + sudo-replace, same as DNS layer.

### Testability

- AppleScript runner is interfaced (`BrowserTabReader`) so the matching logic can be unit-tested with fake tab lists.
- Hostname normalization + match is a pure function: `hostBlocked(url string, blocklist []string) bool` with table-driven tests covering scheme/port/subdomain/path-only cases.
- AppleScript script strings are constants in code (greppable, easy to audit).

## Open design questions

1. **Where in the codebase does the AppleScript runner live?** Options:
   - `internal/infra/browser_monitor.go` — alongside other OS-integration code.
   - New package `internal/browser/`.
     I lean toward `infra/` for KISS — it's the same shape as `freedom.go` and `launchd.go`.

2. **Action on a match**: close-tab vs kill-browser threshold.
   - Close-tab is gentle but easily-defeated by re-opening.
   - Kill-browser is heavier-friction but disruptive (loses other tabs).
   - MVP: close-tab on every match, escalate to kill-browser when threshold ≥3 in one tick.

3. **Hostname normalization specifics**:
   - `https://www.youtube.com/watch?v=…` → host `www.youtube.com` → blocked (exact match in list).
   - `https://m.youtube.com/…` → blocked (we list `m.youtube.com`).
   - `https://yt.be/…` (YouTube short URL) → currently NOT in blocklist → would slip through. Need to extend `DefaultDNSBlocklist` with redirect hostnames separately, OR have the browser monitor follow one redirect before deciding.
   - **Decision**: MVP does exact-host match against `DefaultDNSBlocklist`. Short-URL redirects are a separate follow-up.

4. **What about private browsing / Incognito?** AppleScript on Chrome lists Incognito windows by default; Safari Private Browsing windows are NOT enumerated unless explicitly allowed. **Decision**: rely on AppleScript's defaults. If the user opens a Private/Incognito window to bypass, that's a known partial gap; document it and consider as Phase 2 with a network-layer block.

5. **Brave Sync / Chrome Sync via signed-in account** — could a user sync their tab to a different device and view it there? Yes, but out of scope for the appmon project (single-machine protection).

## Compatibility with existing protection layers

This feature is **complementary**, not replacement, to the DNS layer:

| Layer | Catches | Misses |
|---|---|---|
| DNS (`/etc/hosts`) | All apps that use system resolver | DoH-enabled browsers, IPv6-preferred apps, apps with hardcoded IPs |
| **Browser tab monitor (this feature)** | Any URL that reached a browser tab, regardless of DNS path | Non-browser apps, embedded WebViews |

A blocked URL might:
- Fail DNS → never reach the browser → DNS layer wins. Browser monitor sees nothing.
- Succeed DNS via DoH → reach the browser → tab loads → **browser monitor closes the tab within 15s**.

Both layers are present; either catching it is sufficient.

## CLI surface

New CLI command `appmon browser`:

- `appmon browser` — prints permission status for each supported browser (granted / denied / not running), recent close-tab events from the log.
- No `appmon browser disable` — by design.

`appmon status` gains a "Browser monitor" section similar to the existing "Freedom protection" section.

## Open security / abuse considerations

1. **AppleScript itself is bypassable** — a user could disable Automation permission for `appmon`. We detect and log this, but cannot prevent it at the macOS permission layer without a system extension (deferred to Phase 2).
2. **AppleScript injection** — we don't take user input into AppleScript. All scripts are constants. No injection surface.
3. **Browser crashes the daemon** — bounded timeouts + error logging prevent this.

## Phase 2 / future

- **Firefox via WebExtension**: requires building+signing a Firefox add-on that posts tab URLs to a localhost endpoint exposed by the daemon. Workable but heavier.
- **HTTPS-MITM proxy** (Layer 7): catch URLs at the network level rather than the browser level. Major increase in complexity (cert installation, certificate trust, performance) and ethical surface (proxy-decrypted traffic). Out of MVP.
- **Network Extension (NEFilterControlProvider)**: kernel-level URL block at SNI layer. Requires Apple Developer notarization. Major commitment.
- **Sync-down blocklist updates from a server**: today the blocklist is compiled-in (perm-ban semantic). A server-synced list with the same write-only-by-server semantic would let the user add to (not remove from) the list without rebuilding. Strictly orthogonal to this feature.

## Suggested implementation order

1. **`infra/browser_monitor.go`**: AppleScript constants, `BrowserTabReader` interface, `TabsOf(browser) ([]Tab, error)` per supported browser. Tests with table-driven hostname matching.
2. **Watcher wiring**: new `BrowserScanInterval` field in `WatcherConfig`, new ticker case in `Run()`. Tests update `daemon_test.go`.
3. **`appmon browser` CLI**: status + recent-events view.
4. **README + CHANGELOG**: document Layer 6, the threat model gap it closes, the Automation permission setup step.
5. **End-to-end manual test plan**: open a blocked URL in Safari and confirm tab closes within 15s; same for Chrome and Brave; verify Incognito tabs are correctly handled per design decision.
6. **Release** as v0.7.0 (minor — new defense layer).

---

## Implementation status (2026-05-13)

Shipped as a **standalone bash companion tool** rather than integrated into
the Go daemon, because the primary use case is managed corporate laptops
where the user has no root / no Go runtime / no extension-install rights.

- Implementation: [`app_mon/browser_guard/`](../../app_mon/browser_guard/)
  — `browser_guard.sh` (single self-installer), `list_open_tabs.sh`
  (read-only debug), `README.md` (operator docs).
- Mechanism: AppleScript polling via `osascript`, fired every 10 seconds
  by a LaunchAgent (and every 5 minutes by a cron fallback layer that
  heals the LaunchAgent if it gets unloaded).
- Self-protection: 4 hostname-hash-derived hidden install locations
  (primary script + backup script + LaunchAgent plist + cron entry).
  Each tick heals the others; removing the system requires locating and
  deleting all four within one cron cycle.
- Blocklist source: hardcoded `BLOCKLIST=(...)` array in the script.
  Editing the source and re-running `./browser_guard.sh on`
  force-propagates the change to both installed copies.

Phase-2 items in the body of this doc (MITM proxy, NEFilterControlProvider,
Firefox WebExtension, server-synced blocklist) remain deferred — the
managed-laptop constraint rules out all of them for now.

Notable surprise during implementation: AppleScript identifier shadowing.
Inside a `tell application "Google Chrome"` block, the bareword `tab` is
NOT the ASCII tab character but Chrome's Tab class object. Using `tab` as
a field separator silently produced lines like `Google Chrometabhttps://…`
with no real tab byte, causing the bash `read -r browser url` to receive
empty `$url` and skip every tab. Fixed by defining
`set tabChar to (ASCII character 9)` OUTSIDE the `tell` block so the
browser's dictionary can't shadow it.
