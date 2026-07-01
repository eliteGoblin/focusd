#!/usr/bin/env python3
"""browser_guard.py — no-sudo macOS browser guard (standalone utility).

Kills any browser (Chrome / Brave / Edge / Safari) sitting on a blocklisted
host, via osascript (AppleScript) — no extension, no proxy, no sudo. A
lightweight, user-mode cousin of the `browser-monitor` plugin.

Usage:
  python3 browser_guard.py          scan once: kill browsers on a blocklisted host
  python3 browser_guard.py on       install + auto-run (self-protecting, 4 layers)
  python3 browser_guard.py off      remove the install

Self-protecting install (`on`), all user-level:
  L1+L2  two hidden, mutually-healing script copies
  L3     LaunchAgent  (runs every 10s)
  L4     cron entry   (every 5m, fallback; best-effort — may need Full Disk Access)
Every tick HEALS first (restores any deleted piece from a survivor) then scans,
so removing it means deleting ALL pieces within the cron's 5-min window.

Deps: python3 + /usr/bin/osascript (+ pkill) — all macOS-native. No sudo.
First scan prompts once for Automation permission per browser.
"""
import os
import subprocess
import sys
import time

# ── Blocklist: killed if a tab's host EQUALS an entry or is a subdomain ───────
BLOCKLIST = [
    # search / video
    "google.com", "youtube.com", "bilibili.com",
    # gaming
    "steampowered.com", "steamcommunity.com", "steamcontent.com", "steamstatic.com",
    "dota2.com", "dota.com", "chronodivide.com", "dos.zone", "play-cs.com", "webrcade.com",
    # news / doomscroll
    "9news.com.au", "abc.net.au", "news.com.au", "smh.com.au", "espn.com.au",
    "theaustralian.com.au", "163.com", "iranintl.com", "southcn.com", "tmtpost.com",
    # misc
    "zhihu.com", "heheda.top", "alibaba.com",
]

BROWSERS = ["Google Chrome", "Brave Browser", "Microsoft Edge", "Safari"]

# ── Self-protecting install layout (user-level, disguised) ───────────────────
HOME = os.path.expanduser("~")
COPY_A = os.path.join(HOME, "Library", "Application Support", ".com.apple.mobileassetd.cached")
COPY_B = os.path.join(HOME, "Library", ".com.apple.spotlight.indexed")
COPIES = [COPY_A, COPY_B]
LABEL = "com.apple.coreservices.useractivityd.helper"   # disguised LaunchAgent label
PLIST = os.path.join(HOME, "Library", "LaunchAgents", LABEL + ".plist")
CRON_TAG = "# com.apple.uaidx"                           # marker for our cron line
SCAN_INTERVAL = 10                                       # LaunchAgent cadence (seconds)
LOG = "/tmp/.uaidx.log"


# ── Detect / match / kill ─────────────────────────────────────────────────────
def host_of(url):
    u = url.split("://", 1)[-1].split("/", 1)[0].split("?", 1)[0].split("@")[-1].split(":", 1)[0]
    return u.lower()


def is_blocked(h):
    return any(h == d or h.endswith("." + d) for d in BLOCKLIST)


def _running_apps():
    r = subprocess.run(["/usr/bin/osascript", "-e",
                        'tell application "System Events" to get name of (every process whose background only is false)'],
                       capture_output=True, text=True)
    return set(n.strip() for n in r.stdout.split(","))


def _tabs_of(app):
    s = ('tell application "%s"\nset out to ""\n'
         'repeat with w in windows\nrepeat with t in tabs of w\ntry\n'
         'set u to URL of t\n'
         'if u is not missing value and u is not "" then set out to out & u & linefeed\n'
         'end try\nend repeat\nend repeat\nreturn out\nend tell') % app
    r = subprocess.run(["/usr/bin/osascript", "-e", s], capture_output=True, text=True)
    return [u.strip() for u in r.stdout.splitlines() if u.strip()]


def list_tabs():
    # Separate simple per-browser osascript calls. A single combined script with
    # an `on isRunning` handler silently returns NOTHING when osascript is spawned
    # by python3/launchd (it only works from Terminal) — these calls work everywhere.
    # Gate on running processes so we never poke an uninstalled browser.
    running = _running_apps()
    tabs = []
    for app in BROWSERS:
        if app in running:
            tabs += [(app, u) for u in _tabs_of(app)]
    return tabs


def kill_browser(app):
    subprocess.run(["/usr/bin/osascript", "-e", 'tell application "%s" to quit' % app], capture_output=True)
    time.sleep(1)
    subprocess.run(["/usr/bin/pkill", "-i", "-f", app], capture_output=True)


def scan():
    offenders = set()
    for app, url in list_tabs():
        h = host_of(url)
        if h and is_blocked(h):
            offenders.add(app)
            print("BLOCKED: %s -> %s" % (app, h))
    for app in offenders:
        print("killing: %s" % app)
        kill_browser(app)
    if not offenders:
        print("clean: no blocklisted tabs open")
    return 0


# ── Self-heal / install ───────────────────────────────────────────────────────
def _source_bytes():
    for p in [os.path.abspath(__file__)] + COPIES:
        try:
            with open(p, "rb") as f:
                return f.read()
        except OSError:
            continue
    return None


def _plist_xml(target):
    return ('<?xml version="1.0" encoding="UTF-8"?>\n'
            '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">\n'
            '<plist version="1.0"><dict>\n'
            '  <key>Label</key><string>%s</string>\n'
            '  <key>ProgramArguments</key><array>'
            '<string>/usr/bin/python3</string><string>%s</string></array>\n'
            '  <key>RunAtLoad</key><true/>\n'
            '  <key>StartInterval</key><integer>%d</integer>\n'
            '  <key>StandardOutPath</key><string>%s</string>\n'
            '  <key>StandardErrorPath</key><string>%s</string>\n'
            '</dict></plist>\n') % (LABEL, target, SCAN_INTERVAL, LOG, LOG)


def _crontab():
    return subprocess.run(["crontab", "-l"], capture_output=True, text=True).stdout


def heal():
    data = _source_bytes()
    if data is None:
        return
    for c in COPIES:  # L1 + L2
        if not os.path.exists(c):
            os.makedirs(os.path.dirname(c), exist_ok=True)
            with open(c, "wb") as f:
                f.write(data)
            os.chmod(c, 0o755)
    if not os.path.exists(PLIST):  # L3
        os.makedirs(os.path.dirname(PLIST), exist_ok=True)
        with open(PLIST, "w") as f:
            f.write(_plist_xml(COPY_A))
        subprocess.run(["launchctl", "load", PLIST], capture_output=True)
    cur = _crontab()  # L4 (best-effort; may need Full Disk Access)
    if CRON_TAG not in cur:
        line = "*/5 * * * * /usr/bin/python3 %s >/dev/null 2>&1  %s\n" % (COPY_B, CRON_TAG)
        new = (cur.rstrip("\n") + "\n" + line) if cur.strip() else line
        subprocess.run(["crontab", "-"], input=new, text=True, capture_output=True)


def install():
    # force-overwrite copies + plist from THIS file so `on` always deploys the
    # current version (even over an older install), then load.
    data = _source_bytes()
    if data is not None:
        for c in COPIES:
            os.makedirs(os.path.dirname(c), exist_ok=True)
            with open(c, "wb") as f:
                f.write(data)
            os.chmod(c, 0o755)
    os.makedirs(os.path.dirname(PLIST), exist_ok=True)
    with open(PLIST, "w") as f:
        f.write(_plist_xml(COPY_A))
    heal()
    subprocess.run(["launchctl", "unload", PLIST], capture_output=True)
    subprocess.run(["launchctl", "load", PLIST], capture_output=True)
    print("installed: LaunchAgent (every %ds) + cron (5m) + 2 hidden copies" % SCAN_INTERVAL)
    print("logs: %s   (first scan prompts for Automation permission per browser)" % LOG)


def uninstall():
    subprocess.run(["launchctl", "unload", PLIST], capture_output=True)
    cur = _crontab()
    if CRON_TAG in cur:
        new = "".join(l for l in cur.splitlines(keepends=True) if CRON_TAG not in l)
        subprocess.run(["crontab", "-"], input=new, text=True, capture_output=True)
    for p in [PLIST] + COPIES:
        try:
            os.remove(p)
        except FileNotFoundError:
            pass
    print("uninstalled: LaunchAgent + cron + both copies removed.")


if __name__ == "__main__":
    cmd = sys.argv[1] if len(sys.argv) > 1 else ""
    if cmd == "on":
        install()
    elif cmd == "off":
        uninstall()
    else:
        heal()  # self-heal every tick, then scan
        sys.exit(scan())
