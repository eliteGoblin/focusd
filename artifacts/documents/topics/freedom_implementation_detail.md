# Freedom App Implementation Details

## Overview

This document captures technical details about how Freedom app works on macOS, based on investigation and analysis. Useful for understanding the architecture if building a similar app or porting appmon to Swift.

---

## 🔥 Key Findings (Jan 2025 Investigation)

### Verified Facts From Live System

| Fact | Evidence |
|------|----------|
| **Helper runs as root** | `ps aux` shows `com.80pct.FreedomHelper` as root (PID 815) |
| **Uses SMAppService** | `launchctl print system/com.80pct.FreedomHelper` shows `managed_by = com.apple.xpc.ServiceManagement` |
| **Plist in app bundle** | `/Applications/Freedom.app/Contents/Library/LaunchDaemons/FreedomHelper-Launchd.plist` |
| **NOT in standard location** | No entry in `/Library/LaunchDaemons/` or `/Library/PrivilegedHelperTools/` |
| **XPC MachService** | `com.80pct.FreedomHelper.mach` endpoint active |
| **Proxy on :7769** | `FreedomProxy` runs as user, intercepts HTTP/HTTPS |
| **FreedomProxy is Go** | Binary is compiled Go (universal arm64/x86_64) |

### Helper Capabilities (from strings analysis)

```
/usr/sbin/networksetup        # CLI tool for proxy
-setwebproxy                  # Set HTTP proxy
-setsecurewebproxy            # Set HTTPS proxy
-setwebproxystate             # Enable/disable proxy
-listallnetworkservices       # Enumerate interfaces
Received load custom firewall rules
Received remove custom firewall rules
```

### Modern SMAppService Pattern (macOS 13+)

Freedom uses **SMAppService** (not legacy SMJobBless):

```
App Bundle Structure:
/Applications/Freedom.app/
├── Contents/
│   ├── MacOS/
│   │   ├── Freedom                    # Main app (Swift)
│   │   ├── FreedomProxy               # Proxy server (Go!)
│   │   └── com.80pct.FreedomHelper    # Privileged helper (Swift)
│   ├── Library/
│   │   ├── LaunchDaemons/
│   │   │   └── FreedomHelper-Launchd.plist  # ← Plist stays here!
│   │   └── LaunchServices/
│   │       └── com.80pct.FreedomHelper      # ← Helper binary here
│   └── Info.plist                     # Contains SMPrivilegedExecutables
```

**Key difference from legacy SMJobBless:**
- Plist stays **inside app bundle** (not copied to `/Library/LaunchDaemons/`)
- Uses `SMAppService.daemon()` Swift API
- Still requires admin auth on first install
- Cleaner, Apple-preferred approach for macOS 13+

---

## 🎯 XPC vs Pure Go: Architecture Decision

### Freedom's Architecture (Swift + XPC)

```
┌────────────────────────┐         ┌────────────────────────┐
│   Swift Main App       │   XPC   │   Swift Helper         │
│   (USER space)         │◀───────▶│   (ROOT LaunchDaemon)  │
│   - UI, scheduling     │ Mach    │   - networksetup       │
│   - Policy management  │ Service │   - Minimal code       │
│   - Sandboxed (MAS)    │         │   - ~50KB binary       │
└────────────────────────┘         └────────────────────────┘
         │
         ▼
┌────────────────────────┐
│   FreedomProxy (Go!)   │
│   (USER space)         │
│   - localhost:7769     │
│   - HTTP/HTTPS filter  │
└────────────────────────┘
```

### Appmon's Architecture (Pure Go)

```
┌────────────────────────────────────────┐
│   Go Binary (ROOT LaunchDaemon)        │
│   ┌──────────────┐ ┌──────────────┐   │
│   │   Watcher    │ │   Guardian   │   │
│   │   (root)     │◀│   (root)     │   │
│   │   - Enforce  │ │   - Monitor  │   │
│   │   - Kill     │ │   - Restart  │   │
│   │   - Delete   │ │              │   │
│   └──────────────┘ └──────────────┘   │
└────────────────────────────────────────┘
```

### Why Freedom Uses XPC (Benefits)

| Benefit | Description |
|---------|-------------|
| **Minimal root surface** | Only ~50KB helper runs as root, not entire app |
| ~~**App Store main app**~~ | ~~Main app can be sandboxed~~ **← NOT TRUE: Freedom Mac is DMG only!** |
| **Security audit** | Easy to audit small helper; main app bugs can't escalate |
| **Crash isolation** | Main app crash ≠ helper crash |
| **macOS "blessed"** | Uses Apple's official SMAppService pattern |
| **Clean architecture** | UI/policy in user space, only privileged ops in helper |

### Why Pure Go Is Better For Appmon

| Benefit | Description |
|---------|-------------|
| **Simpler** | No XPC, no Swift, single language |
| **Stronger enforcement** | Everything runs as root = maximum power |
| **Harder to bypass** | No user-space app to attack/kill |
| **Self-healing** | Watcher/Guardian mutual monitoring |
| **Single binary** | Easy deployment via Homebrew |

### XPC Requires Swift?

| Component | Swift Required? | Go Possible? |
|-----------|----------------|--------------|
| SMAppService API | ✅ Yes | ❌ No native binding |
| XPC MachServices | ✅ Yes | ⚠️ CGO possible but painful |
| LaunchDaemon itself | ❌ No | ✅ Yes |
| networksetup calls | ❌ No | ✅ Yes (`exec.Command`) |

### Verdict: When to Use Which

**Use XPC + Swift when:**
- ~~You want App Store distribution~~ **← NOT POSSIBLE for blocking apps!**
- You want minimal root attack surface
- You have complex GUI that shouldn't run as root
- You want Apple's "blessed" architecture
- You're building a Swift/SwiftUI app anyway

**Use Pure Go when:**
- CLI-based tool (no GUI)
- Maximum enforcement is the goal
- Self-protection is critical
- Simpler maintenance preferred
- Single binary deployment

**For appmon: Pure Go is the right choice.**

Even Freedom (the market leader) uses DMG-only distribution because Mac App Store rules prohibit the privileged helper needed for system proxy control.

---

## Freedom Components

| Component | Process Name | Description |
|-----------|--------------|-------------|
| Main App | `Freedom` | User-facing app with UI, scheduling |
| Proxy | `FreedomProxy` | HTTP proxy server |
| Helper | `com.80pct.FreedomHelper` | Privileged helper for system changes |

### Ports Used

```
FreedomProxy:
  - Port 7769: HTTP proxy (traffic interception)
  - Port 7770: RPC (internal communication)
```

### Evidence: Freedom Uses Swift

```bash
$ otool -L /Applications/Freedom.app/Contents/MacOS/Freedom
  /System/Library/Frameworks/Foundation.framework/Versions/C/Foundation
  /System/Library/Frameworks/AppKit.framework/Versions/C/AppKit
  /usr/lib/libobjc.A.dylib
  /usr/lib/libSystem.B.dylib
  @rpath/libswiftAppKit.dylib          # ← Swift runtime
  @rpath/libswiftCore.dylib            # ← Swift runtime
  @rpath/libswiftFoundation.dylib      # ← Swift runtime
```

The presence of `libswift*.dylib` confirms Freedom is written in Swift.

## System Proxy Architecture

### How Freedom Blocks Traffic

```
Browser request → System Proxy (127.0.0.1:7769) → FreedomProxy
                                                      ↓
                                              Check block list
                                                      ↓
                                    ┌─────────────────┴─────────────────┐
                                    ↓                                   ↓
                              Blocked site                        Allowed site
                                    ↓                                   ↓
                              Show block page                    Forward to internet
```

### System Proxy vs Environment Variable

| Type | Scope | Set By |
|------|-------|--------|
| System proxy (`networksetup`) | All apps (browsers, etc.) | FreedomHelper (root) |
| `HTTP_PROXY` env var | Only terminal/CLI tools | User |

Freedom sets **system proxy**, which affects all GUI apps. Terminal commands like `curl` do NOT use system proxy by default.

### Verifying System Proxy

```bash
# Check current proxy settings
$ networksetup -getwebproxy Wi-Fi
Enabled: Yes
Server: 127.0.0.1
Port: 7769

# Check all interfaces
$ networksetup -listallnetworkservices
Wi-Fi
Ethernet
Thunderbolt Bridge
```

### How Proxy Is Set (Per Interface)

Freedom sets proxy on ALL network interfaces:
```bash
# What FreedomHelper does internally:
networksetup -setwebproxy "Wi-Fi" 127.0.0.1 7769
networksetup -setsecurewebproxy "Wi-Fi" 127.0.0.1 7769
networksetup -setwebproxy "Ethernet" 127.0.0.1 7769
# ... for all interfaces
```

## FreedomHelper (Privileged Helper)

### Purpose

FreedomHelper runs as root to perform privileged operations that the main app (running as user) cannot do:
- Set/unset system proxy (requires root)
- Modify network configuration

### How It's Installed: SMAppService (Modern Pattern)

Freedom uses **SMAppService** (macOS 13+), not legacy SMJobBless:

```swift
// In Freedom.app (user space) - modern API
import ServiceManagement

let service = SMAppService.daemon(plistName: "FreedomHelper-Launchd.plist")
try service.register()  // Prompts for admin auth
```

**Key difference from legacy SMJobBless:**
- Helper binary stays in app bundle: `Contents/Library/LaunchServices/`
- Plist stays in app bundle: `Contents/Library/LaunchDaemons/`
- NOT copied to `/Library/LaunchDaemons/` or `/Library/PrivilegedHelperTools/`
- Managed by `com.apple.xpc.ServiceManagement`

```
# Verified: No files in standard locations
$ ls /Library/LaunchDaemons/ | grep freedom    # Empty
$ ls /Library/PrivilegedHelperTools/ | grep freedom    # Empty

# But helper IS running as root
$ launchctl print system/com.80pct.FreedomHelper
  managed_by = com.apple.xpc.ServiceManagement
  path = (submitted by smd.514)
  pid = 815
```

### Communication: XPC

```
Freedom.app (user) ──XPC──► FreedomHelper (root)
                              │
                              ├── "setProxyOn"  → networksetup -setwebproxy ...
                              ├── "setProxyOff" → networksetup -setwebproxystate off
                              └── Other privileged operations
```

XPC (Cross-Process Communication) is Apple's secure IPC mechanism.

### What Helper Does NOT Do

Based on testing, FreedomHelper does **NOT**:
- Monitor if Freedom.app is running
- Restart Freedom.app if killed
- Protect against process termination

Evidence:
```bash
$ killall Freedom
# Freedom stays dead, does NOT auto-restart
# FreedomHelper continues running but doesn't restart the app
```

## Process Protection (NONE)

### Key Finding: Freedom Is NOT Protected

| Action | Result |
|--------|--------|
| Kill Freedom.app | Stays dead |
| Kill FreedomProxy | Stays dead |
| Kill FreedomHelper | Stays dead (until reboot) |

Freedom relies on:
1. User not wanting to kill it
2. Login Items to restart on reboot

### Login Items vs LaunchAgent

Freedom uses **Login Items**, not LaunchAgent:

```bash
# Check Login Items
$ osascript -e 'tell application "System Events" to get the name of every login item'
Freedom
```

| Aspect | Login Items | LaunchAgent |
|--------|-------------|-------------|
| Visibility | System Settings > Login Items | Hidden in ~/Library/LaunchAgents |
| Apple preference | ✅ Recommended for apps | For background services |
| User can disable | Easy toggle | Need to delete plist |
| App Store | Required | Not allowed to hide |

### Why Freedom Uses Login Items

1. App Store requirement (transparency)
2. Apple's recommended way for user-facing apps
3. Freedom is a "friendly" app, not self-enforcing like appmon

## Technology Stack

### Frameworks Used

| Framework | Purpose |
|-----------|---------|
| Swift | Main language |
| AppKit | UI (menu bar, windows) |
| SystemConfiguration | Network proxy settings |
| ServiceManagement | SMJobBless for helper |
| XPC | Communication with helper |

### SystemConfiguration Framework

Used for network monitoring and proxy configuration:
```swift
import SystemConfiguration

// Set proxy programmatically
let preferences = SCPreferencesCreate(nil, "Freedom" as CFString, nil)
// ... modify network configuration
SCPreferencesApplyChanges(preferences)
```

FreedomHelper likely uses:
- `SCNetworkProtocolSetConfiguration()` - set proxy config
- `SCPreferencesApplyChanges()` - apply changes

## 🚨 Important: Freedom for Mac is NOT on Mac App Store

| Platform | Distribution | Mechanism |
|----------|--------------|-----------|
| **Mac** | **DMG only** | Privileged helper + system proxy |
| **iOS** | App Store | VPN profile / Screen Time API |
| **Android** | Play Store | VPN profile |
| **Windows** | EXE installer | Similar to Mac |

**Why no Mac App Store version:**
- Mac App Store requires sandboxing
- Sandboxed apps **cannot** install privileged helpers
- Cannot modify system proxy without root
- Freedom's core protection (system proxy) is incompatible with App Store rules

**Implication for appmon:** Don't worry about App Store compatibility. Freedom's architecture proves that serious Mac blocking apps must use DMG/direct download with privileged helper.

---

## Comparison: Freedom vs appmon

| Feature | Freedom | appmon |
|---------|---------|--------|
| Language | Swift + Go (proxy) | Go |
| Blocking method | HTTP proxy | Process kill + file delete |
| Privileged ops | SMAppService helper | LaunchDaemon (root) |
| Auto-restart | ❌ No | ✅ Yes (mutual monitoring) |
| Hidden from user | ❌ No (Login Items) | ✅ Yes (obfuscated) |
| Distribution | **DMG only** | Homebrew |
| System proxy | ✅ Yes (networksetup) | ⬜ Can add easily |

## What Appmon CAN Do (Same as Freedom, in Pure Go)

### System Proxy Control

```go
// Set system proxy (requires root) - same as FreedomHelper
func SetSystemProxy(host string, port int) error {
    interfaces := []string{"Wi-Fi", "Ethernet", "USB 10/100/1000 LAN"}

    for _, iface := range interfaces {
        // HTTP proxy
        exec.Command("networksetup", "-setwebproxy", iface,
            host, fmt.Sprintf("%d", port)).Run()
        // HTTPS proxy
        exec.Command("networksetup", "-setsecurewebproxy", iface,
            host, fmt.Sprintf("%d", port)).Run()
    }
    return nil
}

// Remove proxy
func RemoveSystemProxy() error {
    interfaces := []string{"Wi-Fi", "Ethernet", "USB 10/100/1000 LAN"}

    for _, iface := range interfaces {
        exec.Command("networksetup", "-setwebproxystate", iface, "off").Run()
        exec.Command("networksetup", "-setsecurewebproxystate", iface, "off").Run()
    }
    return nil
}
```

### Local Proxy Server (Like FreedomProxy)

```go
// FreedomProxy is actually Go! We can do the same
import "github.com/elazarl/goproxy"

func StartProxy() {
    proxy := goproxy.NewProxyHttpServer()

    proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
        if isBlocked(req.URL.Host) {
            return req, goproxy.NewResponse(req, "text/html", 403, blockPage)
        }
        return req, nil
    })

    http.ListenAndServe(":7769", proxy)
}
```

### What Appmon Already Does Better Than Freedom

| Capability | Freedom | Appmon |
|------------|---------|--------|
| Kill blocked processes | ❌ No | ✅ Yes |
| Delete app files | ❌ No | ✅ Yes |
| Uninstall via brew | ❌ No | ✅ Yes |
| Self-healing (watcher/guardian) | ❌ No | ✅ Yes |
| Obfuscated process names | ❌ No | ✅ Yes |
| Binary self-restore | ❌ No | ✅ Yes |

## Considerations for Future Swift Port

### What Would Change

| Component | Go (current) | Swift (future) |
|-----------|--------------|----------------|
| Daemon architecture | LaunchDaemon (root) | LaunchAgent + SMJobBless helper |
| IPC | File-based registry | XPC |
| Process control | `syscall.Kill()` | `kill()` or `Process()` |
| Proxy (if added) | `networksetup` CLI | SystemConfiguration framework |
| UI (if added) | None | SwiftUI menu bar |

### What Would Stay Same

- Core blocking logic (kill processes, delete files)
- Mutual monitoring pattern (can implement in helper)
- Server-side code (keep as Go)

### SMJobBless Requirements

To use privileged helper pattern:
1. Code signing with Developer ID
2. SMPrivilegedExecutables in Info.plist
3. SMAuthorizedClients in helper's Info.plist
4. Matching team IDs and bundle identifiers

### Why Port to Swift?

Only if you need:
1. App Store distribution
2. Native macOS UI (SwiftUI menu bar)
3. SystemConfiguration framework (native proxy control)

For CLI + Homebrew distribution, **Go is sufficient**.

## Limitations of Freedom

1. **Domain-level blocking only** - Cannot block specific URLs or paths
2. **No subdomain whitelist** - Block google.com blocks everything including cloud.google.com
3. **No content analysis** - Cannot detect videos/news on unknown sites
4. **No process protection** - Easy to kill
5. **Visible in Login Items** - Easy to disable

These are opportunities for a competing product.

## Potential Features for Replacement App (Own Proxy)

With your own HTTP proxy, you have **full control** over every request. You can inspect:
- Full URL (domain + path + query parameters)
- Request headers
- Response content (for LLM analysis)

### Per-URL/Subdomain Blocking

```
Google:
  ✗ google.com/search*              ← blocked (distracting search)
  ✗ news.google.com                 ← blocked (news)
  ✓ cloud.google.com                ← allowed (GCP console)
  ✓ console.cloud.google.com        ← allowed (GCP)
  ✓ docs.google.com                 ← allowed (work docs)

Amazon:
  ✗ amazon.com                      ← blocked (shopping, time-restricted)
  ✗ amazon.com/s?*                  ← blocked (product search)
  ✓ kindle.amazon.com               ← allowed (reading)
  ✓ read.amazon.com                 ← allowed (Kindle Cloud Reader)
  ✓ aws.amazon.com                  ← allowed (work)

YouTube:
  ✗ youtube.com                     ← blocked by default
  ✓ youtube.com/watch?v=WHITELIST   ← specific videos allowed
  ✓ youtube.com/@EducationalChannel ← specific channels allowed
```

### Enforce SafeSearch

Your proxy can **rewrite requests** to force SafeSearch:

```
Incoming:  google.com/search?q=cats
Rewritten: google.com/search?q=cats&safe=active

# Or for Bing:
Incoming:  bing.com/search?q=dogs
Rewritten: bing.com/search?q=dogs&safeSearch=strict
```

User cannot disable SafeSearch because proxy always adds the parameter.

### Time-Based Restrictions

```go
// In your proxy
func handleRequest(url string) Decision {
    if isAmazonShopping(url) {
        if isWorkHours() {  // 9am-5pm weekdays
            return BLOCK
        }
        if todayUsage("amazon") > 30*time.Minute {
            return BLOCK  // Max 30 min/day
        }
    }
    return ALLOW
}
```

### LLM-Based Content Analysis

```
User visits: obscure-news-site.com/article
    ↓
Proxy intercepts response HTML
    ↓
LLM analyzes: "Page contains: autoplay video, news content, clickbait headlines"
    ↓
Decision: Block (matches user's "no news/video" policy)
```

**Implementation options:**
- **Local LLM**: Ollama + Llama 3.2 (private, no API cost, ~3GB RAM)
- **Managed API**: OpenAI/Claude (better accuracy, ~$0.001/page)
- **Hybrid**: Local for quick checks, API for uncertain cases

### Proxy Implementation in Go

```go
func main() {
    proxy := goproxy.NewProxyHttpServer()

    proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
        url := req.URL.String()

        // Per-URL blocking
        if shouldBlock(url) {
            return req, goproxy.NewResponse(req, "text/html", 403, blockPage)
        }

        // Enforce SafeSearch
        if isGoogleSearch(url) {
            req.URL.RawQuery = addSafeSearch(req.URL.RawQuery)
        }

        return req, nil
    })

    // For LLM analysis, intercept responses
    proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
        if needsLLMAnalysis(ctx.Req.URL) {
            content := readBody(resp)
            if llmDetectsHarmful(content) {
                return blockedResponse(ctx.Req)
            }
        }
        return resp
    })

    log.Fatal(http.ListenAndServe(":7769", proxy))
}
```

### What Freedom CAN'T Do (Your Advantage)

| Feature | Freedom | Your Proxy |
|---------|---------|------------|
| Block google.com/search but allow GCP | ❌ | ✅ |
| Enforce SafeSearch always | ❌ | ✅ |
| Time-restrict Amazon shopping | ❌ | ✅ |
| Allow specific YouTube videos | ❌ | ✅ |
| LLM content analysis | ❌ | ✅ |
| Whitelist subdomains | ❌ | ✅ |

## References

- SMJobBless documentation: https://developer.apple.com/documentation/servicemanagement/1431078-smjobbless
- SystemConfiguration framework: https://developer.apple.com/documentation/systemconfiguration
- XPC Services: https://developer.apple.com/documentation/xpc
- LaunchDaemons: https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html
