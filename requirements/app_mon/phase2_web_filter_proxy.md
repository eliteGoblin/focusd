# Web Filter Proxy

**Status**: Planned (Phase 1: CONNECT proxy)
**Related**: Replace Freedom app with custom solution

---

## Problem Statement

Current app blocking (process killing, file deletion) doesn't provide web filtering. Need to:
- Block distracting domains (google.com search) while allowing productive ones (console.cloud.google.com)
- Block shopping sites (amazon.com) while allowing reading (kindle.amazon.com)
- Time-based rules for email (mail.google.com only during work hours)

---

## Key Insight: CONNECT Proxy Solves Most Use Cases

**HTTPS CONNECT proxy can see domain AND subdomain** - this is sufficient for most blocking needs:

```
Browser: CONNECT console.cloud.google.com:443 HTTP/1.1

Proxy sees:
├── ✅ Full domain: console.cloud.google.com
├── ✅ Subdomain distinction: www.google.com vs console.cloud.google.com
├── ✅ Port: 443
├── ❌ Path: /projects/... (encrypted)
├── ❌ Query params: ?q=... (encrypted)
```

**This means we CAN:**
- Block `google.com` and `www.google.com` (search)
- Whitelist `console.cloud.google.com` (GCP)
- Whitelist `maps.google.com` (always useful)
- Time-based rules for `mail.google.com`

**No MITM required** for domain/subdomain filtering.

---

## Requirements

### Core Filtering (CONNECT Proxy - In Scope)

- Block by **domain**: `twitter.com`, `reddit.com`, `google.com`
- Block by **subdomain**: `www.amazon.com` but allow `kindle.amazon.com`
- **Whitelist** specific subdomains: `console.cloud.google.com`, `maps.google.com`
- **Time-based rules**: `mail.google.com` only 9am-12pm, 2pm-5pm on weekdays
- Whitelist mode: Allow only specific sites during focus sessions

### Enforcement

- Self-enforcing (user wants to block themselves)
- Hard to bypass (not just a browser extension)
- Works across all browsers (Chrome, Safari, Firefox)
- Works for Electron apps that use system proxy
- Survives browser incognito mode

### Coexistence

- Work alongside corporate VPN (GlobalProtect, etc.)
- Chain to corporate proxy when on corp network
- Don't break banking/financial sites
- Don't break corporate internal sites

### Out of Scope (MITM Features)

These require MITM proxy - **not worth the complexity**:
- Block by path: `youtube.com/shorts/*`
- Block by query param: `google.com/search?q=*`
- AI analysis of browsing behavior

See [Appendix: MITM Proxy Research](#appendix-mitm-proxy-research) for technical details.

---

## High-Level Solution

### Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│  Laptop                                                             │
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  appmon CONNECT Proxy (localhost:8888)                      │   │
│  │                                                              │   │
│  │  1. Receive CONNECT request (domain visible!)               │   │
│  │  2. Check domain against rules                              │   │
│  │     → Block: reject CONNECT, return error page              │   │
│  │     → Allow: establish tunnel (passthrough)                 │   │
│  │  3. No TLS termination - just tunnel bytes                  │   │
│  │                                                              │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  PAC File (~/.appmon/proxy.pac)                             │   │
│  │                                                              │   │
│  │  - Route all traffic to localhost:8888                      │   │
│  │  - Bypass localhost, local network                          │   │
│  │                                                              │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  appmon Daemon (existing)                                   │   │
│  │                                                              │   │
│  │  - Monitor proxy settings, restore if changed               │   │
│  │  - Ensure proxy process running                             │   │
│  │                                                              │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  NO Root CA needed! (no MITM = no cert issues)                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Why CONNECT Proxy (Not MITM)

| Aspect | CONNECT Proxy | MITM Proxy |
|--------|---------------|------------|
| Domain blocking | ✅ | ✅ |
| Subdomain blocking | ✅ | ✅ |
| Path blocking | ❌ | ✅ |
| Query param blocking | ❌ | ✅ |
| Root CA required | ❌ No | ✅ Yes |
| Cert pinning issues | ❌ None | ✅ Breaks apps |
| Corp laptop friendly | ✅ Easy | ⚠️ Complex |
| Implementation complexity | Low | High |
| **ROI** | **High** | **Low** |

**Conclusion**: CONNECT proxy gives 90% of the value with 10% of the complexity.

### Components

| Component | Purpose | Implementation |
|-----------|---------|----------------|
| CONNECT Proxy | Tunnel & filter by domain | Go + `net/http` |
| PAC Generator | Create/update PAC file | Go template |
| Rule Engine | Evaluate block/allow rules | Go, YAML config |
| Daemon Integration | Monitor & enforce | Existing appmon daemon |

### Filtering Rules Format

```yaml
# ~/.appmon/filter-rules.yaml

rules:
  # Block entire domain (including www subdomain)
  - domain: "twitter.com"
    action: block
  - domain: "www.twitter.com"
    action: block

  # Block google.com search, whitelist useful subdomains
  - domain: "google.com"
    action: block
  - domain: "www.google.com"
    action: block
  - domain: "console.cloud.google.com"  # GCP - always allow
    action: allow
  - domain: "maps.google.com"           # Maps - always allow
    action: allow
  - domain: "mail.google.com"           # Gmail - time-based
    action: allow
    schedule:
      weekdays: "09:00-12:00,14:00-17:00"

  # Block shopping, allow reading
  - domain: "amazon.com"
    action: block
  - domain: "www.amazon.com"
    action: block
  - domain: "kindle.amazon.com"         # Reading - allow
    action: allow
  - domain: "aws.amazon.com"            # Work - allow
    action: allow

  # Social media - block during work hours
  - domain: "reddit.com"
    action: block
    schedule:
      weekdays: "09:00-17:00"

upstream_proxy:
  # Auto-detect or manual for corp environments
  auto_detect: true
  # manual: "http://corpproxy.internal:8080"
```

### Request Flow

```
Request: https://www.google.com/search?q=distraction

1. Browser → PAC → "use localhost:8888"

2. Browser → Proxy: CONNECT www.google.com:443

3. Proxy checks rules:
   - Domain: www.google.com
   - Rule: block
   - Action: REJECT

4. Proxy → Browser: 403 Blocked
   (Shows motivational quote page)

---

Request: https://console.cloud.google.com/projects

1. Browser → Proxy: CONNECT console.cloud.google.com:443

2. Proxy checks rules:
   - Domain: console.cloud.google.com
   - Rule: allow
   - Action: TUNNEL

3. Proxy establishes tunnel to console.cloud.google.com:443
   Browser ←→ Proxy ←→ Google (encrypted, proxy can't see content)
```

---

## Implementation Phases

### Phase 1: Basic CONNECT Proxy (Target)
- HTTP/HTTPS CONNECT proxy (no MITM)
- Domain and subdomain blocking
- PAC file generation
- Upstream proxy detection for corp environments

### Phase 2: Advanced Rules
- Time-based rules
- Whitelist/focus modes
- Rule reload without restart

### Phase 3: Integration
- Daemon monitors proxy process
- Daemon restores proxy settings if changed
- CLI commands: `appmon proxy on/off/status`

### Future (Maybe Never): MITM
- Only if path/query blocking becomes critical need
- See appendix for research

---

## Technical Considerations

### PAC File

**Scope:**
```
PAC file affects:
├── ✅ Safari (always uses system proxy)
├── ✅ Chrome (uses system by default)
├── ✅ Edge (uses system by default)
├── ✅ macOS apps using URLSession
├── ❌ Firefox (has own proxy settings)
├── ❌ Terminal (curl, wget, git)
└── ❌ Most CLI tools
```

**Terminal requires env vars:**
```bash
export HTTP_PROXY=http://127.0.0.1:8888
export HTTPS_PROXY=http://127.0.0.1:8888
export NO_PROXY=localhost,127.0.0.1
```

### VPN Interaction

VPN operates at network layer (Layer 3), proxy at application layer (Layer 7):

```
Browser → localhost:8888 (your proxy) → VPN tunnel → Internet
                ↑
        Local loopback (VPN doesn't see this)
```

- `localhost` traffic never leaves machine
- Proxy's OUTBOUND connections go through VPN automatically
- No special handling needed - VPN is transparent to proxy

### Corporate Proxy Coexistence

**Solution:** Proxy chaining - your proxy forwards to corp proxy:

```
Browser → localhost:8888 (your proxy) → corpproxy:8080 (via VPN) → Internet
```

**Detection strategy:**
1. Check `$HTTP_PROXY` / `$HTTPS_PROXY` env vars
2. Read existing system PAC file before replacing
3. Query WPAD if available

### PAC File Replacement Strategy

**Problem:** macOS only allows ONE PAC file. If corp has a PAC, we must replace it.

```
BEFORE (corp PAC):
  Browser → Corp PAC → decides proxy routing

AFTER (our PAC):
  Browser → Our PAC → always use localhost:8888
  Our Proxy → duplicates corp PAC logic → routes to corp proxy or direct
```

**Corp PAC typically does:**
```javascript
// Example corp PAC file logic
function FindProxyForURL(url, host) {
    // Internal sites - direct
    if (shExpMatch(host, "*.internal.corp.com")) return "DIRECT";
    if (shExpMatch(host, "*.intranet.corp.com")) return "DIRECT";
    if (isInNet(host, "10.0.0.0", "255.0.0.0")) return "DIRECT";

    // Everything else - corp proxy
    return "PROXY corpproxy.internal:8080";
}
```

**Our approach:**

1. **On install**: Read and parse existing corp PAC file
2. **Extract rules**: Which hosts go DIRECT vs PROXY
3. **Store config**: Save corp routing rules to `~/.appmon/upstream-rules.yaml`
4. **Replace PAC**: Install our PAC that routes everything to localhost:8888
5. **Proxy logic**: Our proxy applies corp's routing rules for upstream

```yaml
# ~/.appmon/upstream-rules.yaml (auto-generated from corp PAC)

# Extracted from corp PAC on 2026-01-29
upstream:
  default: "http://corpproxy.internal:8080"

  direct:  # These bypass corp proxy (go direct)
    - "*.internal.corp.com"
    - "*.intranet.corp.com"
    - "10.0.0.0/8"
    - "192.168.0.0/16"
    - "localhost"
    - "127.0.0.1"
```

**Request flow with corp proxy:**

```
Request: https://www.google.com (blocked)

1. Browser → Our PAC → localhost:8888
2. Our Proxy: domain=www.google.com → BLOCK
3. Return 403

---

Request: https://external-service.com (allowed, needs corp proxy)

1. Browser → Our PAC → localhost:8888
2. Our Proxy: domain=external-service.com → ALLOW
3. Our Proxy checks upstream-rules.yaml:
   - Not in direct list → use corpproxy.internal:8080
4. Our Proxy → Corp Proxy → Internet

---

Request: https://wiki.internal.corp.com (allowed, internal)

1. Browser → Our PAC → localhost:8888
2. Our Proxy: domain=wiki.internal.corp.com → ALLOW
3. Our Proxy checks upstream-rules.yaml:
   - Matches *.internal.corp.com → DIRECT
4. Our Proxy → Direct → Internal Server
```

**Implementation notes:**

- PAC files are JavaScript - need simple parser for common patterns
- Handle: `shExpMatch()`, `isInNet()`, `dnsDomainIs()`, `DIRECT`, `PROXY`
- Re-scan corp PAC periodically (corp may update it)
- Fallback: if can't parse, prompt user for manual upstream config

---

## Open Questions

- How to handle Firefox (separate proxy settings)?
- Should proxy run as daemon or separate process?
- How to enforce proxy settings can't be changed?

---

## References

- Go net/http for CONNECT proxy
- macOS proxy settings: `networksetup` commands
- PAC file spec: https://developer.mozilla.org/en-US/docs/Web/HTTP/Proxy_servers_and_tunneling/Proxy_Auto-Configuration_PAC_file

---

---

# Appendix: MITM Proxy Research

> **Status**: Research only. Not planned for implementation.
>
> **Why not?** MITM adds significant complexity (Root CA, cert pinning issues, corporate compatibility) for marginal benefit. CONNECT proxy solves 90% of use cases.

## What MITM Enables

| Feature | CONNECT Proxy | MITM Proxy |
|---------|---------------|------------|
| Block by path | ❌ | ✅ `youtube.com/shorts/*` |
| Block by query param | ❌ | ✅ `google.com/search?q=*` |
| See full URL for logging | ❌ | ✅ |
| AI analysis of behavior | ❌ | ✅ |

## Why MITM is Problematic

1. **Root CA Required**: Must generate and install CA certificate
2. **Certificate Pinning**: Banking apps, 1Password, etc. will break
3. **Corporate Laptops**: IT policies may detect/block custom CAs
4. **Browser Trust**: Firefox has separate cert store
5. **Maintenance Burden**: Cert expiry, security concerns

## MITM Technical Details (For Reference)

### Certificate Hierarchy

```
Root CA (installed in keychain, never sent to browser)
    │
    │ Signs
    ▼
Per-Domain Certificates (generated dynamically, cached)
    ├── youtube.com cert
    ├── twitter.com cert
    └── reddit.com cert
```

### TLS Termination

MITM proxy maintains two independent TLS connections:

```
Browser ◄──── TLS Connection 1 ────► Your Proxy
               (YOUR cert)                │
                                     Decrypt
                                          │
                                     Plaintext HTTP
                                     (you can read)
                                          │
                                     Re-encrypt
                                          │
          Your Proxy ◄──── TLS Connection 2 ────► Destination
                           (DESTINATION's cert)
```

### Certificate Pinning Bypass

Apps with cert pinning reject proxy certificates. Need bypass list:

```yaml
bypass:
  - "*.bankofamerica.com"
  - "*.chase.com"
  - "*.commbank.com.au"
  - "*.apple.com"
  - "*.1password.com"
```

### goproxy Library (If Ever Needed)

```go
package main

import (
    "log"
    "net/http"
    "github.com/elazarl/goproxy"
)

func main() {
    proxy := goproxy.NewProxyHttpServer()

    // Enable MITM
    proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)

    // Access full HTTP request (decrypted)
    proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
        log.Println("Path:", req.URL.Path)      // /shorts/abc
        log.Println("Query:", req.URL.RawQuery) // v=123

        if shouldBlock(req.URL) {
            return req, goproxy.NewResponse(req,
                goproxy.ContentTypeHtml,
                http.StatusForbidden,
                "<h1>Blocked</h1>")
        }
        return req, nil
    })

    log.Fatal(http.ListenAndServe(":8888", proxy))
}
```

**What goproxy handles:**
- TLS termination and re-encryption
- Per-domain certificate generation
- Certificate caching
- HTTP request/response parsing
- Upstream proxy support
