# Freedom App Protection

**GitHub Issue**: [#6](https://github.com/eliteGoblin/focusd/issues/6)

## Overview

Protect Freedom app (freedom.to) from being disabled. Freedom blocks distracting websites via HTTP proxy but has NO self-protection - killing it leaves it dead.

## Problem Statement

Freedom relies on:
1. User not wanting to kill it
2. Login Items to restart on reboot

This is a gap appmon can fill - protect Freedom like it protects itself.

## Freedom Architecture (Verified)

| Component | Process Name | Purpose | Run As |
|-----------|--------------|---------|--------|
| Main App | `Freedom` | UI, scheduling, starts proxy | User |
| Proxy | `FreedomProxy` | HTTP proxy (ports 7769, 7770) | User |
| Helper | `com.80pct.FreedomHelper` | System proxy settings | Root |

### Key Finding

```bash
$ killall Freedom
# Freedom stays dead - does NOT auto-restart
# FreedomHelper continues running but doesn't restart the app
```

## User Story

As a user who uses both appmon and Freedom, I want appmon to protect Freedom from being disabled so I can't bypass my own distraction blocking.

## Protection Features

### 1. Process Restart (Priority: High)

Monitor and restart Freedom.app if killed:

```bash
# Detection: check if process exists
pgrep -x "Freedom" || echo "Freedom not running"

# Restart
open -a "/Applications/Freedom.app"
```

- Check interval: 5 seconds (same as self-protection)
- FreedomProxy starts automatically when Freedom starts

### 2. Login Item Protection (Priority: High)

Ensure Freedom remains in macOS Login Items:

```bash
# Check if present
osascript -e 'tell application "System Events" to get the name of every login item' | grep -q "Freedom"

# Restore if missing
osascript -e 'tell application "System Events" to make login item at end with properties {path:"/Applications/Freedom.app", hidden:false}'
```

- Check interval: 60 seconds

### 3. Health Reporting (Priority: Medium)

Report Freedom status in `appmon status`:

```
Feature Health:
  ✓ Freedom protection  Freedom.app running, proxy active, login item present
```

Or degraded states:
```
  ⚠ Freedom protection  Helper missing (reinstall Freedom to fix)
  ⚠ Freedom protection  Proxy not responding on port 7769
  ✗ Freedom protection  Freedom.app not installed
```

## What appmon CAN Do

| Protection | Implementation |
|------------|----------------|
| Restart Freedom.app | `open -a Freedom` if process missing |
| Monitor FreedomProxy | Check process exists (restarts with Freedom) |
| Restore Login Items | AppleScript to add back if removed |
| Report status | Show in `appmon status` |

## What appmon CANNOT Do

| Limitation | Reason |
|------------|--------|
| Install FreedomHelper | Requires SMJobBless from signed Freedom.app |
| Modify system proxy | FreedomHelper's job, requires XPC |
| Protect Freedom's schedule | Freedom stores this internally |

## Implementation

### New Protection Policy

```go
// internal/policy/freedom.go
type FreedomProtector struct {
    appPath      string   // "/Applications/Freedom.app"
    processes    []string // ["Freedom", "FreedomProxy"]
    helperPath   string   // "/Library/PrivilegedHelperTools/com.80pct.FreedomHelper"
    proxyPort    int      // 7769
}

func (f *FreedomProtector) IsInstalled() bool
func (f *FreedomProtector) IsRunning() bool
func (f *FreedomProtector) Restart() error
func (f *FreedomProtector) IsLoginItemPresent() bool
func (f *FreedomProtector) RestoreLoginItem() error
func (f *FreedomProtector) HealthStatus() HealthStatus
```

### Integration with Watcher

```go
// In watcher loop, after app blocking enforcement:
if freedomProtector.IsInstalled() && !freedomProtector.IsRunning() {
    logger.Info("Freedom killed, restarting")
    freedomProtector.Restart()
}
```

## Edge Cases

| Scenario | Behavior |
|----------|----------|
| Freedom not installed | Skip protection, no error |
| Freedom installed but never opened | Don't start it (user may not want it) |
| Freedom running, killed | Restart within 5s |
| Login Item removed | Restore within 60s |
| Helper missing | Warn in status, continue protecting app |
| User in "allowed time" window | Still protect Freedom (it manages its own schedule) |

## Definition of Done

- [ ] Freedom.app auto-restarts within 5s if killed
- [ ] FreedomProxy monitored (restarts with Freedom)
- [ ] Login Items restored if removed (check every 60s)
- [ ] `appmon status` shows Freedom protection health
- [ ] Graceful handling if Freedom not installed (skip, don't error)
- [ ] Graceful degradation if helper missing (warn in status)
- [ ] Unit tests for FreedomProtector
- [ ] Integration test: kill Freedom, verify restart

## Out of Scope (Future)

- Protecting Freedom's block schedule/settings
- Preventing Freedom from being uninstalled
- Managing system proxy directly
- Coordinating appmon blocking with Freedom sessions

## Dependencies

- **Depends on**: [#5 Feature Health Status System](https://github.com/eliteGoblin/focusd/issues/5) - for status reporting

## References

- Technical analysis: `artifacts/documents/topics/freedom_implementation_detail.md`
- Freedom website: https://freedom.to
