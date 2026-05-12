# browser_guard

User-mode browser tab guard for macOS. Watches every open tab in Chrome / Brave / Safari and SIGKILLs any browser holding a tab whose host matches an embedded blocklist. Sibling tool to the Go `appmon` daemon, but standalone — no sudo, no Go runtime, just bash + macOS AppleScript + launchd + cron.

## Why this exists

`appmon`'s existing DNS layer (`/etc/hosts` blackhole) leaks in three places:

- **DoH** — Chrome / Brave / Firefox each ship with DNS-over-HTTPS as a 1-click setting that bypasses `/etc/hosts` entirely.
- **IPv6 leak** — `/etc/hosts` blocks only `A` records (IPv4). Dual-stack domains (youtube.com etc.) still resolve via real DNS for their `AAAA` record.
- **Hardcoded IPs / private resolvers** — some apps bypass the system resolver entirely.

For browsers, the URL bar is also the user's path-of-least-resistance bypass: toggle DoH, open the blocked URL anyway. `browser_guard` reads the URL bar via AppleScript — downstream of any DNS bypass. If a tab is open with a blocked host, the browser gets killed regardless of how it resolved DNS.

`browser_guard` is **user-mode only**, so it works on managed/corp laptops without admin or root. Browser-extension approaches were ruled out for the same reason (can't install extensions on a managed device).

## Files

| File | Purpose |
|---|---|
| `browser_guard.sh` | Single self-installing script. `on` deploys all four self-protection layers; default action (called by launchd + cron) does heal + scan. |
| `list_open_tabs.sh` | Read-only debug script. Lists every open tab in Chrome / Brave / Safari. No kills, no install. Use to verify AppleScript is granted Automation permission. |

## Install

```sh
./browser_guard.sh on
```

That's the entire surface area. There is **no `off` / `uninstall` command** by design — friction is the point.

On first run after install, macOS prompts once per running browser for Automation permission ("com.apple.cfprefsd.helper.<hex> wants to control Google Chrome"). Approve each. Subsequent scans are silent.

The install command:

1. Hashes `"browser-guard-<hostname>"` to derive obfuscated paths (deterministic per machine, opaque per anyone else).
2. Force-copies the script to **two** hidden install locations:
   - `~/.cache/.com.apple.metadata.<hash>/com.apple.metadata.helper.<hash>` (primary)
   - `~/Library/Application Support/.com.apple.coreservices.<hash>/com.apple.coreservices.daemon.<hash>` (backup)
3. Force-writes a LaunchAgent plist at `~/Library/LaunchAgents/com.apple.cfprefsd.helper.<hash>.plist` (label looks like a macOS XPC helper).
4. Adds a crontab entry `*/5 * * * *` with an idempotent marker comment.
5. `launchctl unload`+`load` so the agent picks up any changes (e.g. updated `SCAN_INTERVAL`).

After install, both installed copies + the plist + the cron entry are independent. Delete any single one → the next tick of any other layer restores it. The user must locate AND delete all four within one cron cycle (5 minutes) to fully remove the system.

`on` is **idempotent**: re-running it after editing the source script propagates the changes to both installed copies and reloads the LaunchAgent. This is the workflow for updating the blocklist.

## The 4 layers (self-healing)

| Layer | What it does | Fires |
|---|---|---|
| **Primary script** | Real executable invoked by launchd + cron | On every tick |
| **Backup script** | Independent copy at a different hidden path | Heals primary if it's missing |
| **LaunchAgent plist** | Fires the script every 10s | macOS launchd |
| **Cron entry** | Fires the script every 5 min (heals LaunchAgent if unloaded) | `/usr/sbin/cron` |

Every invocation runs `heal_all` BEFORE scanning. Heal is idempotent — stat's all four pieces, restores any missing one from any surviving copy. If only ONE script copy survives, it can re-derive the other three on its next 10-second tick.

```
        ┌─────────────────┐
        │ primary script  │◄─────────┐
        └────────┬────────┘          │
                 │ invoked by        │ heal
                 ▼                   │
        ┌─────────────────┐    ┌─────┴───────┐
        │  LaunchAgent    │    │ backup copy │
        │    every 10s    │    └─────┬───────┘
        └─────────────────┘          │ heal-from-survivor
                                     │
        ┌─────────────────┐          │
        │   cron entry    │──────────┘
        │   every 5 min   │
        └─────────────────┘
```

## What it kills

Per scan tick the script reads URLs via AppleScript from every running browser:

- **Google Chrome** — includes Incognito windows by default (Chrome exposes them to AppleScript).
- **Brave Browser** — same dialect as Chrome (Chromium fork).
- **Safari** — normal windows only. Private Browsing windows are NOT exposed to AppleScript (deliberate macOS restriction). The DNS layer in the main `appmon` daemon covers this gap because Safari Private mode still uses the system resolver.

For each URL it extracts the host, compares to the embedded `BLOCKLIST` array (case-insensitive, exact-OR-subdomain match), and if any tab matches → `osascript`-quits the browser, then `pkill -i -f <browser>` to catch renderer / GPU / network subprocesses.

## Updating the blocklist

```sh
# 1. Edit the BLOCKLIST=(...) array near the top of browser_guard.sh
vim browser_guard.sh

# 2. Re-run install — force-copies the new version to both installed paths
./browser_guard.sh on

# 3. Within ~10 seconds, the next LaunchAgent tick uses the new blocklist
tail -f ~/.cache/.com.apple.metadata.*/.log
```

The blocklist is a flat list of apex domains. Each apex matches itself AND any subdomain — `youtube.com` covers `youtube.com`, `www.youtube.com`, `m.youtube.com`, `music.youtube.com`, etc. Don't need to enumerate subdomains.

The list is **compiled into the script** by design (the perm-ban semantic). To remove a domain you must edit the source AND propagate via `on`, same friction as adding one. There is no separate config file.

## Verify it's running

```sh
# LaunchAgent loaded?
launchctl list | grep 'com.apple.cfprefsd.helper'

# Cron entry present?
crontab -l | grep '# bg-'

# What's the log doing?
tail -f ~/.cache/.com.apple.metadata.*/.log
```

The log shows:
- `BLOCKED browser=<browser> host=<host>` whenever a tab on the blocklist is detected.
- `killing browser=<browser>` immediately after.
- `restored <thing>` whenever heal had to fix a missing piece.

A healthy install has the log mostly empty in steady state — heal is idempotent, so when nothing is missing and no blocked tabs are open, no log lines are written.

## How it complements `appmon` proper

`appmon` (the Go daemon, this directory's parent) provides:

- **Process layer** — kill blocked apps (Steam, Dota 2).
- **Filesystem layer** — delete blocked app bundles + caches.
- **DNS layer** — `/etc/hosts` blackhole for known hosts.
- **Plist + Binary self-protection** — system-mode launchd with relocator-based killall resistance.

`browser_guard` adds:

- **Browser tab layer** — reads URLs from open tabs and kills the browser if any is on the blocklist. Catches the gap when an app (browser) ignores `/etc/hosts` via DoH or AAAA records, or when a tab is opened directly via the URL bar.

The two systems share **no state** and **no code**. They can be installed independently. The Go `appmon` is system-mode (sudo). `browser_guard` is user-mode (no sudo). Running both gives the deepest defense.

## Known limitations

- **Safari Private Browsing**: AppleScript does not enumerate Safari Private windows. Mitigation: rely on the DNS layer in `appmon` proper (Safari Private uses the system resolver). Documented in the requirements doc.
- **Firefox**: no AppleScript surface for tabs without a custom WebExtension. Out of scope (managed laptops can't install extensions).
- **Automation permission denial**: if the user denies the macOS Automation prompt for a browser, the scan for that browser silently returns nothing. The script can't force the prompt to reappear; user must re-enable in System Settings → Privacy & Security → Automation.
- **Cron + Full Disk Access on Catalina+**: `/usr/sbin/cron` may need FDA to actually run. If it doesn't, the LaunchAgent alone handles blocking + heal. The cron layer is "best-effort second mechanism".
- **No version sync with the Go binary**: `browser_guard.sh` versioning is independent. The Go `CHANGELOG.md` does not track this script.

## Removing the system

Not documented. Required by design.

See the source for which paths and labels are involved; everything is hostname-hash-derived, so you can re-derive them by reading the script. Removing all four layers inside one 5-minute cron window is doable but takes deliberate effort — that's the cool-off the system is engineered to impose.

## Related

- `../README.md` — main `appmon` daemon (Go).
- `../../requirements/monitor_browser_tab_url/monitor_web_browser_tab_url.md` — original design doc + threat model + Phase 2 ideas (MITM proxy, NEFilterControlProvider, Firefox WebExtension — none implemented; all deferred or rejected for the managed-laptop use case).
