# mac-browser-guard (utility)

A standalone, **no-sudo** macOS browser guard: it quits any browser
(Chrome / Brave / Edge / Safari) sitting on a **blocklisted host**, using only
`osascript` (AppleScript) + `pkill` — no extension, no proxy, no admin.

This is a lightweight, user-mode **utility** cousin of the `browser-monitor`
plugin. The plugin is the maintained enforcement path (signed binary, driven by
the platform); this single Python file is handy for a quick guard on a plain Mac.

## Use

```bash
# one scan (kill browsers currently on a blocklisted host)
python3 browser_guard.py

# install + auto-run in the background, then it runs itself
python3 browser_guard.py on

# remove it
python3 browser_guard.py off
```

First scan prompts once for **Automation** permission per browser (approve it —
no sudo). Edit the `BLOCKLIST` list at the top to change which hosts trigger a
kill; matching is exact-or-subdomain (`m.youtube.com` matches `youtube.com`).

## What `on` installs (all user-level)

- two hidden, mutually-healing copies of the script
- a **LaunchAgent** that runs every 10s
- a **cron** entry every 5m as a fallback (best-effort — modern macOS may need
  Full Disk Access for cron; the LaunchAgent is the primary path)

Every run self-heals first (restores any deleted piece from a survivor) then
scans — so fully removing it means deleting all pieces within the 5-min window,
or just running `off`.

## Notes

- **Detection quirk:** it uses **separate simple `osascript` calls** per browser.
  A single combined AppleScript with an `on isRunning` handler silently returns
  nothing when `osascript` is spawned by python3/launchd (works only from
  Terminal) — the per-browser calls work in every context.
- Deps: `python3` + `/usr/bin/osascript` + `pkill`, all macOS-native.
- Not for allowlisted/managed Macs where script execution is controlled — get it
  approved through the proper channel there rather than working around controls.
