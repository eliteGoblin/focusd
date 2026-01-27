# Freedom App Protection

## Overview
Protect Freedom app to work in combination with appmon for comprehensive distraction blocking.

## Freedom Components
| Component | Process | Port | Run As |
|-----------|---------|------|--------|
| Main App | Freedom | - | User |
| Proxy | FreedomProxy | 7769 (HTTP), 7770 (RPC) | User |
| Helper | com.80pct.FreedomHelper | - | Root |

## Key Finding
Freedom does NOT auto-restart when killed. FreedomHelper only handles proxy settings (via SystemConfiguration framework), not process monitoring.

## Protection Features

### 1. Login Item Protection (Priority: High)
- Monitor if Freedom is in macOS Login Items
- Restore if removed using AppleScript:
  ```bash
  osascript -e 'tell application "System Events" to make login item at end with properties {path:"/Applications/Freedom.app", hidden:false}'
  ```
- Check interval: 60 seconds

### 2. Process Restart (Priority: Medium)
- Monitor Freedom and FreedomProxy processes
- Restart if killed:
  ```bash
  open -a Freedom
  ```
- Check interval: 30 seconds

### 3. System Proxy Protection (Priority: Low)
- Monitor system proxy settings via `networksetup -getwebproxy`
- Restore if disabled (when Freedom session active)
- Requires root access

## Technical Notes
- Freedom uses Swift + SystemConfiguration framework
- FreedomHelper installed via SMJobBless (Apple's privileged helper mechanism)
- Go can achieve similar proxy control via `networksetup` CLI (requires root)
