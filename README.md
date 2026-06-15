# FocusD - Freedom from Distraction

> A multi-layer defense system against addictive apps and websites on macOS.

## Why

Modern apps are designed like slot machines - engineered to capture attention and create compulsive usage. FocusD creates multiple layers of protection to help you stay focused.

## Multi-Layer Defense

```
┌─────────────────────────────────────────────────────────────┐
│  Layer 1: DNS Blocking (NextDNS)                            │
│  - Blocks gaming domains at network level                   │
│  - Works on ALL devices (Mac, iPhone, iPad)                 │
│  - Can't be bypassed without router access                  │
└─────────────────────────────────────────────────────────────┘
                            │
┌─────────────────────────────────────────────────────────────┐
│  Layer 2: App/Site Blocking (daemon + platform + plugins)   │
│  - Kills Steam/Dota2 processes automatically                │
│  - Host-file + pfctl packet blocking                        │
│  - Self-protecting single launchd mesh                      │
└─────────────────────────────────────────────────────────────┘
```

---

## Layer 1: DNS Blocking (NextDNS)

Blocks gaming domains at the network level for all devices on your network.

**Blocked domains:**
- `steampowered.com`, `steamcommunity.com`, `steamstatic.com`
- All Steam CDN and authentication servers

**Setup:** [NextDNS Setup Guide](artifacts/managed_dns/next_dns.md)

---

## Layer 2: App/Site Blocking (daemon + platform + plugins)

A self-protecting **daemon** keeps a cross-platform **platform** engine running;
the platform enforces policy through **plugins** (kill Steam/Dota2, host-file and
pfctl blocking, Claude-skill re-injection, browser-tab guarding).

**Features:**
- Kills blocked processes on a schedule
- Host-file + pfctl packet blocking of gaming domains
- Self-protecting single launchd mesh with path-rotating self-update
- Drives toward a signed desired state (enforcement is tighten-only)
- No stop command (intentional friction)

**Quick Start:**
```bash
# Build the platform (bundles plugins first)
./scripts/build-platform.sh
```

**Documentation:** see [`requirements/`](requirements/) (features, ADRs, register).

---

## Platform Support

| Platform | Status |
|----------|--------|
| macOS | Supported |
| Windows | Planned |
| Linux | Not planned |

---

## Project Structure

```
focusd/
├── daemon/                     # Layer-1 supervisor (Go) — keeps platform running
├── platform/                   # Cross-platform protection engine (Go)
├── plugins/                    # Enforcement plugins (Go)
├── artifacts/
│   └── managed_dns/
│       └── next_dns.md         # NextDNS setup guide
├── requirements/               # Product/BA docs: features, ADRs, register
└── archive/                    # Deprecated tools
    └── chrome/                 # Chrome extension enforcer (deprecated)
```

## License

MIT
