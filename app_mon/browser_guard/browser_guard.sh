#!/usr/bin/env bash
# browser_guard.sh — single-file, user-mode browser tab guard for macOS.
#
# Layered self-protection (all user-mode, no sudo needed):
#
#   Layer 1: primary script copy at hidden path A
#   Layer 2: backup script copy at hidden path B
#   Layer 3: LaunchAgent plist with obfuscated com.apple.* label
#            (fires every 10s; runs the scan + heal)
#   Layer 4: cron entry (fires every 5 min; runs heal + scan as fallback
#            in case the LaunchAgent gets unloaded)
#
# Every invocation (LaunchAgent OR cron) heals first, then scans. Heal is
# idempotent — stat's all four pieces, restores any one missing from any
# surviving copy. To fully remove, user must locate AND delete all four
# pieces inside the cron's 5-minute window. Missing any single piece →
# the next tick of the other layer restores it.
#
# USAGE
#   ./browser_guard.sh on          one-time install (sets up all four layers)
#   (no further commands — by design)
#
# PERMISSION
#   First scan after install: macOS prompts for Automation permission for
#   each running browser. Approve once per browser; the prompt is per-parent-
#   process so it sticks. To inspect: System Settings → Privacy & Security →
#   Automation.
#
#   Cron may need /usr/sbin/cron to have Full Disk Access on Catalina+ to run
#   at all. macOS still ships cron but flags it deprecated; this script
#   installs it best-effort. If cron silently doesn't run, the LaunchAgent
#   alone still handles blocking + heal.

set -u

# ─── Embedded blocklist (case-insensitive, exact OR subdomain match) ────

BLOCKLIST=(
    # Google search (main distraction surface)
    google.com

    # Video / streaming
    youtube.com
    bilibili.com

    # Gaming
    steampowered.com
    steamcommunity.com
    steamcontent.com
    steamstatic.com
    dota2.com
    dota.com
    chronodivide.com
    dos.zone
    play-cs.com
    webrcade.com

    # News / doomscroll
    9news.com.au
    abc.net.au
    news.com.au
    smh.com.au
    espn.com.au
    theaustralian.com.au
    163.com
    iranintl.com
    southcn.com
    tmtpost.com

    # Misc
    zhihu.com
    heheda.top
    alibaba.com
)

# ─── Obfuscated install paths derived from hostname-hash ────────────────
#
# SHA-1 of "browser-guard-<hostname>" gives a deterministic 40-char hex
# string. Slice into 8-char chunks so different paths don't share suffix.
# All names follow the com.apple.* XPC helper pattern.

_hash() {
    printf '%s' "browser-guard-$(hostname)" | shasum | awk '{print $1}'
}
HASH="$(_hash)"

INSTALL_DIR_A="${HOME}/.cache/.com.apple.metadata.${HASH:0:8}"
INSTALL_SCRIPT_A="${INSTALL_DIR_A}/com.apple.metadata.helper.${HASH:8:8}"

INSTALL_DIR_B="${HOME}/Library/Application Support/.com.apple.coreservices.${HASH:16:8}"
INSTALL_SCRIPT_B="${INSTALL_DIR_B}/com.apple.coreservices.daemon.${HASH:24:8}"

PLIST_LABEL="com.apple.cfprefsd.helper.${HASH:32:8}"
PLIST_PATH="${HOME}/Library/LaunchAgents/${PLIST_LABEL}.plist"

LOG_FILE="${INSTALL_DIR_A}/.log"

CRON_MARKER="# bg-${HASH:0:8}"
CRON_LINE="*/5 * * * * ${INSTALL_SCRIPT_A} check >/dev/null 2>&1  ${CRON_MARKER}"

SCAN_INTERVAL=10  # LaunchAgent fire cadence (seconds)

# ─── Logging ────────────────────────────────────────────────────────────

log() {
    mkdir -p "$(dirname "$LOG_FILE")" 2>/dev/null
    printf '%s %s\n' "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" "$*" >> "$LOG_FILE" 2>/dev/null
}

# ─── URL → host extraction + blocklist match ─────────────────────────────

extract_host() {
    local url="${1#*://}"
    url="${url%%/*}"
    url="${url%%\?*}"
    url="${url%%:*}"
    printf '%s' "$url" | tr '[:upper:]' '[:lower:]'
}

is_blocked() {
    local host="$1"
    [ -z "$host" ] && return 1
    local entry
    for entry in "${BLOCKLIST[@]}"; do
        if [ "$host" = "$entry" ] || [[ "$host" == *."$entry" ]]; then
            return 0
        fi
    done
    return 1
}

# ─── AppleScript: list tabs from each browser ────────────────────────────

list_chromium_tabs() {
    local app="$1"
    # IMPORTANT: define `tabChar` OUTSIDE the `tell application` block.
    # Inside `tell application "Google Chrome"` (and Brave, and Safari),
    # the bareword `tab` is shadowed by the browser's Tab class object,
    # so `& tab &` concatenates the literal string "tab" instead of an
    # ASCII tab — and the bash side reads empty fields and silently skips
    # every URL. Using (ASCII character 9) from outer scope dodges the
    # shadowing.
    osascript <<APPLESCRIPT 2>/dev/null
set tabChar to (ASCII character 9)
if application "$app" is running then
    tell application "$app"
        set output to ""
        repeat with w in windows
            repeat with t in tabs of w
                try
                    set u to URL of t
                    if u is missing value then set u to ""
                    if u is not "" then set output to output & "$app" & tabChar & u & linefeed
                end try
            end repeat
        end repeat
        return output
    end tell
end if
APPLESCRIPT
}

list_safari_tabs() {
    osascript <<'APPLESCRIPT' 2>/dev/null
set tabChar to (ASCII character 9)
if application "Safari" is running then
    tell application "Safari"
        set output to ""
        repeat with w in windows
            repeat with t in tabs of w
                try
                    set u to URL of t
                    if u is missing value then set u to ""
                    if u is not "" then set output to output & "Safari" & tabChar & u & linefeed
                end try
            end repeat
        end repeat
        return output
    end tell
end if
APPLESCRIPT
}

# ─── Kill action ────────────────────────────────────────────────────────

kill_browser() {
    local browser="$1"
    log "killing browser=$browser"
    # Graceful quit first (browser flushes state in <1s).
    osascript -e "tell application \"$browser\" to quit" 2>/dev/null
    sleep 1
    # Force-kill survivors. `-f` matches full command line, catching
    # renderer / GPU / network-service subprocesses.
    pkill -i -f "$browser" 2>/dev/null
}

# ─── Single scan pass ────────────────────────────────────────────────────

scan_once() {
    local killed=""
    {
        list_chromium_tabs "Google Chrome"
        list_chromium_tabs "Brave Browser"
        list_safari_tabs
    } | while IFS=$'\t' read -r browser url; do
        [ -z "$url" ] && continue
        local h
        h="$(extract_host "$url")"
        if is_blocked "$h"; then
            log "BLOCKED browser=$browser host=$h"
            # Dedup so we don't quit the same browser repeatedly per scan.
            if [[ "$killed" != *"|$browser|"* ]]; then
                kill_browser "$browser"
                killed="$killed|$browser|"
            fi
        fi
    done
}

# ─── Self-healing: restore any missing layer ────────────────────────────

# Pick a live script source: prefer current $0, fall back to either surviving copy.
_pick_source() {
    # If we're already running from a known location, $0 is fine.
    if [ -r "$0" ]; then printf '%s' "$0"; return; fi
    if [ -r "$INSTALL_SCRIPT_A" ]; then printf '%s' "$INSTALL_SCRIPT_A"; return; fi
    if [ -r "$INSTALL_SCRIPT_B" ]; then printf '%s' "$INSTALL_SCRIPT_B"; return; fi
    printf ''
}

ensure_script_copies() {
    local src
    src="$(_pick_source)"
    [ -z "$src" ] && { log "no surviving script source — cannot heal"; return; }

    if [ ! -x "$INSTALL_SCRIPT_A" ]; then
        mkdir -p "$INSTALL_DIR_A"
        chmod 700 "$INSTALL_DIR_A"
        cp "$src" "$INSTALL_SCRIPT_A" 2>/dev/null && chmod 700 "$INSTALL_SCRIPT_A" && log "restored primary script"
    fi
    if [ ! -x "$INSTALL_SCRIPT_B" ]; then
        mkdir -p "$INSTALL_DIR_B"
        chmod 700 "$INSTALL_DIR_B"
        cp "$src" "$INSTALL_SCRIPT_B" 2>/dev/null && chmod 700 "$INSTALL_SCRIPT_B" && log "restored backup script"
    fi
}

write_plist() {
    mkdir -p "${HOME}/Library/LaunchAgents"
    cat > "$PLIST_PATH" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${PLIST_LABEL}</string>
    <key>ProgramArguments</key>
    <array>
        <string>${INSTALL_SCRIPT_A}</string>
        <string>check</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StartInterval</key>
    <integer>${SCAN_INTERVAL}</integer>
    <key>StandardOutPath</key>
    <string>${LOG_FILE}</string>
    <key>StandardErrorPath</key>
    <string>${LOG_FILE}</string>
    <key>ProcessType</key>
    <string>Background</string>
</dict>
</plist>
PLIST
    chmod 644 "$PLIST_PATH"
}

ensure_plist() {
    if [ ! -f "$PLIST_PATH" ]; then
        write_plist
        launchctl load "$PLIST_PATH" 2>/dev/null
        log "restored plist + reloaded"
        return
    fi
    # Plist file exists; verify it's loaded into launchd.
    if ! launchctl list "$PLIST_LABEL" >/dev/null 2>&1; then
        launchctl load "$PLIST_PATH" 2>/dev/null
        log "plist file present but agent not loaded — loaded"
    fi
}

ensure_cron() {
    local current
    current="$(crontab -l 2>/dev/null || true)"
    if ! printf '%s' "$current" | grep -qF "$CRON_MARKER"; then
        {
            [ -n "$current" ] && printf '%s\n' "$current"
            printf '%s\n' "$CRON_LINE"
        } | crontab - 2>/dev/null && log "restored cron entry"
    fi
}

heal_all() {
    ensure_script_copies
    ensure_plist
    ensure_cron
}

# ─── Force-overwrite (for explicit `on` reinstall) ──────────────────────
#
# Heal is create-if-missing only (called every 10s, must be cheap and
# not churn). `on` is the explicit user-driven reinstall — it should
# OVERWRITE installed copies from the current source so blocklist edits
# (and any other script changes) actually take effect.

force_install_scripts() {
    local src
    src="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
    [ -r "$src" ] || { log "force-install: source $src unreadable"; return 1; }

    mkdir -p "$INSTALL_DIR_A"; chmod 700 "$INSTALL_DIR_A"
    cp "$src" "$INSTALL_SCRIPT_A" && chmod 700 "$INSTALL_SCRIPT_A"

    mkdir -p "$INSTALL_DIR_B"; chmod 700 "$INSTALL_DIR_B"
    cp "$src" "$INSTALL_SCRIPT_B" && chmod 700 "$INSTALL_SCRIPT_B"

    log "force-installed both script copies from source $src"
}

force_install_plist() {
    write_plist
    # Unload+load so a changed SCAN_INTERVAL or label is picked up.
    # Unload may fail if it wasn't loaded; we don't care.
    launchctl unload "$PLIST_PATH" 2>/dev/null || true
    launchctl load "$PLIST_PATH" 2>/dev/null
    log "force-installed plist + reloaded"
}

# ─── Install (first-time setup OR explicit update) ──────────────────────

install_all() {
    force_install_scripts
    force_install_plist
    ensure_cron       # cron line content is stable, only needs presence
    heal_all          # belt-and-suspenders: ensure everything's in place

    echo "browser-guard installed (4 self-protecting layers)."
    echo
    echo "  primary script : $INSTALL_SCRIPT_A"
    echo "  backup script  : $INSTALL_SCRIPT_B"
    echo "  plist label    : $PLIST_LABEL"
    echo "  plist path     : $PLIST_PATH"
    echo "  cron entry     : */5 * * * *  (marker: $CRON_MARKER)"
    echo "  log            : $LOG_FILE"
    echo "  scan every     : ${SCAN_INTERVAL}s (LaunchAgent) + 5min (cron)"
    echo
    echo "First scan fires now (RunAtLoad). On the FIRST AppleScript call"
    echo "to a browser, macOS will prompt for Automation permission —"
    echo "approve once per browser. Subsequent scans run silently."
    echo
    echo "Tail the log:   tail -f \"$LOG_FILE\""
    echo "Verify agent:   launchctl list | grep \"$PLIST_LABEL\""
}

# ─── Entry point ────────────────────────────────────────────────────────

case "${1:-check}" in
    on)
        install_all
        ;;
    *)
        # Default: heal + scan. Called every 10s by LaunchAgent and
        # every 5min by cron. Both routes call the same action so
        # either alone restores the system.
        heal_all
        scan_once
        ;;
esac
