#!/usr/bin/env bash
# list_open_tabs.sh
#
# Standalone prototype for the browser-tab-URL monitor.
# See: requirements/monitor_browser_tab_url/monitor_web_browser_tab_url.md
#
# Lists every URL currently open in Google Chrome / Brave Browser / Safari
# via AppleScript. Read-only — does NOT close tabs, kill processes, or
# modify anything. Purpose: verify the AppleScript approach actually
# returns the tabs you see in your browser, BEFORE wiring this into the
# appmon daemon.
#
# Output format (one tab per line):
#
#     [Chrome] Page Title — https://example.com/path
#
# ─────────────────────────────────────────────────────────────────
# FIRST RUN: macOS will pop up "Terminal wants permission to control
# Google Chrome" (or Brave / Safari). Click OK. The prompt appears
# once per browser. To inspect or revoke later:
#
#     System Settings → Privacy & Security → Automation →
#     Terminal → Chrome / Brave / Safari
#
# If you've already granted, no prompt and tabs print immediately.
# ─────────────────────────────────────────────────────────────────
#
# Usage:
#     chmod +x list_open_tabs.sh
#     ./list_open_tabs.sh
#
# Exit:
#     0 — at least one tab was listed
#     1 — no supported browser is running, or no permission granted

set -u

# ─── helpers ────────────────────────────────────────────────────

# list_chromium: prints URLs from a Chromium-family browser (Chrome or
# Brave — both use the same AppleScript dialect since Brave is a fork).
#
# The `if application X is running` precheck is critical: a bare
# `tell application "X"` would LAUNCH X if it isn't running, and would
# trigger the Automation permission prompt for browsers you don't use.
# `is running` is a non-tell query handled by the AppleScript runtime,
# so it does neither.
list_chromium() {
    local app="$1"
    osascript <<APPLESCRIPT
if application "$app" is running then
    tell application "$app"
        set output to ""
        repeat with w in windows
            repeat with t in tabs of w
                try
                    set u to URL of t
                    if u is missing value then set u to "<no-url>"
                    set ti to title of t
                    if ti is missing value then set ti to "<no-title>"
                    set output to output & "[$app] " & ti & " — " & u & linefeed
                end try
            end repeat
        end repeat
        return output
    end tell
end if
APPLESCRIPT
}

# list_safari: prints URLs from Safari. Different terminology than
# Chromium — Safari calls it `name of tab` instead of `title of tab`.
#
# Known limitation: Safari does NOT enumerate Private Browsing windows
# via AppleScript. This is a deliberate macOS-level restriction and
# represents a known gap in the protection model. Documented in the
# requirements doc § "Incognito / Private".
list_safari() {
    osascript <<'APPLESCRIPT'
if application "Safari" is running then
    tell application "Safari"
        set output to ""
        repeat with w in windows
            repeat with t in tabs of w
                try
                    set u to URL of t
                    if u is missing value then set u to "<no-url>"
                    set ti to name of t
                    if ti is missing value then set ti to "<no-title>"
                    set output to output & "[Safari] " & ti & " — " & u & linefeed
                end try
            end repeat
        end repeat
        return output
    end tell
end if
APPLESCRIPT
}

# ─── main ───────────────────────────────────────────────────────

found_any=0

for spec in 'chrome:Google Chrome' 'brave:Brave Browser' 'safari:'; do
    name="${spec%%:*}"
    appname="${spec#*:}"
    case "$name" in
        safari) out=$(list_safari) ;;
        *)      out=$(list_chromium "$appname") ;;
    esac
    if [ -n "$out" ]; then
        printf '%s' "$out"
        found_any=1
    fi
done

if [ "$found_any" -eq 0 ]; then
    {
        echo "list_open_tabs.sh: no browser tabs found."
        echo ""
        echo "Possible reasons:"
        echo "  1. No supported browser is running (Chrome / Brave / Safari)."
        echo "  2. Automation permission has not been granted for this terminal."
        echo "     Check: System Settings → Privacy & Security → Automation"
        echo "  3. All open browsers have zero tabs (unlikely)."
    } >&2
    exit 1
fi
