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
│  Layer 2: App Blocking (appmon)                             │
│  - Kills Steam/Dota2 processes automatically                │
│  - Deletes app files if reinstalled                         │
│  - Self-protecting dual-daemon architecture                 │
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

## Layer 2: App Blocking (appmon)

Automatically kills processes and deletes files for blocked apps (Steam, Dota2).

**Features:**
- Kills blocked processes every 10 minutes
- Deletes app bundles and game files
- Self-protecting dual-daemon (watcher + guardian)
- Auto-starts on login via LaunchAgent
- No stop command (intentional friction)

**Quick Start:**
```bash
cd app_mon
make build
./build/appmon start
```

**Documentation:** [app_mon README](app_mon/README.md)

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
├── app_mon/                    # App blocking daemon
├── artifacts/
│   └── managed_dns/
│       └── next_dns.md         # NextDNS setup guide
├── requirements/               # Feature specifications
└── archive/                    # Deprecated tools
    └── chrome/                 # Chrome extension enforcer (deprecated)
```

## License

MIT
