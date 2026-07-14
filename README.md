# FocusD - Freedom from Distraction

> A multi-layer defense system against addictive apps and websites on macOS.

## Why

Modern apps are designed like slot machines - engineered to capture attention and create compulsive usage. FocusD creates multiple layers of protection to help you stay focused.

## Supervision + Enforcement Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  Layer 1: Daemon (self-protecting supervisor)               │
│  - Keeps platform running (mutually-respawning mesh)        │
│  - Ed25519-verifies platform before execution               │
│  - Out-of-band companion for recovery                       │
└─────────────────────────────────────────────────────────────┘
                            │
┌─────────────────────────────────────────────────────────────┐
│  Layer 2: Platform (protection engine)                      │
│  - Runs plugins on schedule (idempotent reconcile loop)     │
│  - Ed25519-verifies plugins before execution                │
│  - Enforces tighten-only config (can't disable protections) │
└─────────────────────────────────────────────────────────────┘
                            │
┌─────────────────────────────────────────────────────────────┐
│  Layer 3: Plugins (enforcement units)                       │
│  - Kill Steam/Dota2 (kill-steam)                            │
│  - Host-file blocking (dns-block)                           │
│  - Packet filtering (network-block)                         │
│  - Claude skill re-injection (skill-protector)              │
│  - Browser tab blocking (browser-monitor)                   │
│  - Freedom app auto-launch (freedom-protector)              │
└─────────────────────────────────────────────────────────────┘
```

---

## How It Works

A **daemon** (Layer 1 supervisor) keeps a cross-platform **platform** engine (Layer 2) running and verifies it hasn't been tampered with. The **platform** runs **plugins** (Layer 3) on schedule, continuously re-verifying each plugin hasn't been swapped or deleted before execution. Each layer protects the one below it: if the platform is modified, the daemon restores it; if a plugin is tampered with, the platform restores it.

**Key properties:**
- **Anti-tamper:** Every binary is Ed25519-signed; each supervisor re-verifies before execution
- **Self-recovering:** Baked fallback version, out-of-band companion recovery, single-instance convergence
- **Invisible:** Working directory derived from binary location (off argv/env), neutral process names
- **Tighten-only:** Config can only add restrictions, never disable existing ones
- **No stop command:** Intentional friction; only ritual-gated override path

**Setup:** Install requires an explicit version tag (`install -v vX.Y.Z`) so you choose when to adopt each release.

---

## Plugins

| Plugin | Purpose | Runs as |
|--------|---------|---------|
| **kill-steam** | Terminates Steam/Dota2 processes; removes app data each run | system/user |
| **dns-block** | Writes `/etc/hosts` blocklist for gaming domains | system/user |
| **network-block** | pfctl packet filtering (IP-level blocking) | system only |
| **skill-protector** | Re-injects Claude refusal skill if removed | system/user |
| **browser-monitor** | Quits browser windows on blocklisted sites | system/user |
| **freedom-protector** | Keeps Freedom focus app running (optional, requires license) | user mode |

**Quick Start:**
```bash
# Build the platform (bundles plugins first)
./scripts/build-platform.sh

# Run tests
cd daemon   && go test -race ./...
cd platform && go test -race ./...
```

**Documentation:** See [`requirements/`](requirements/) for features, decisions (ADRs), and acceptance test history.

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
├── daemon/                     # Layer-1 supervisor (Go) — keeps platform alive + verified
├── platform/                   # Layer-2 protection engine (Go) — runs plugins + verifies them
├── plugins/                    # Layer-3 plugins (Go) — enforcement units
│   ├── kill-steam/
│   ├── dns-block/
│   ├── network-block/
│   ├── skill-protector/
│   ├── browser-monitor/
│   └── freedom-protector/
├── requirements/               # Product/BA docs: features, decisions (ADRs), test history
├── artifacts/                  # Reference guides
│   └── managed_dns/next_dns.md # Optional: NextDNS router-level blocking
├── scripts/                    # Build and test helpers
└── archive/                    # Deprecated tools (Chrome extension, etc.)
```

## License

MIT
