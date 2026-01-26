# App Monitor (app_mon) - High-Level Design

## Problem Statement

**User**: Developer/admin with full sudo access on macOS
**Challenge**: Gaming addiction (Dota 2/Steam) that cannot be solved by willpower alone
**Constraint**: Cannot remove admin access (required for work)

**Goal**: Build a self-binding system that creates enough friction that the "urge-driven self" cannot easily bypass, while the "rational self" can manage when truly needed.

---

## Core Principle

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                                                                             │
│   Since you have admin access, ABSOLUTE PREVENTION IS IMPOSSIBLE.          │
│                                                                             │
│   The goal is:                                                              │
│   1. RAISE THE COST of bypassing (time, effort, cognitive dissonance)      │
│   2. AUTO-RECOVERY - even if bypassed, protection restores itself          │
│   3. DEFENSE IN DEPTH - bypassing one layer doesn't defeat all             │
│   4. EXPLOIT URGE PSYCHOLOGY - urges peak at ~10min then fade              │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        MULTI-LAYER DEFENSE SYSTEM                           │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ LAYER 1: EXTERNAL DNS (NextDNS)                        [IMPLEMENTED]│   │
│  │ • Router configured with NextDNS                                    │   │
│  │ • Mac agent keeps linked IP updated                                 │   │
│  │ • Blocks: steampowered.com, steamcommunity.com, etc.               │   │
│  │ • Bypass: Log into NextDNS dashboard (external friction)           │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ LAYER 2: LOCAL DNS (/etc/hosts)                            [PLANNED]│   │
│  │ • Daemon maintains blocked domains in /etc/hosts                    │   │
│  │ • Auto-restores if file is modified                                 │   │
│  │ • Backup if NextDNS is bypassed                                     │   │
│  │ • Bypass: Kill daemon + edit file (friction)                        │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ LAYER 3: NETWORK FIREWALL (macOS pf)                       [PLANNED]│   │
│  │ • Block Steam/Valve IP ranges at packet level                       │   │
│  │ • Works even if DNS is bypassed (direct IP access)                  │   │
│  │ • Bypass: Modify pf rules (requires knowing IP ranges)              │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ LAYER 4: APPLICATION ENFORCEMENT                           [PLANNED]│   │
│  │ • Process monitor: Kill Steam/Dota2 on sight                        │   │
│  │ • File watcher: Delete game files when detected                     │   │
│  │ • Bypass: Kill daemon first (but guardian restarts it)              │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ LAYER 5: GUARDIAN SYSTEM                                   [PLANNED]│   │
│  │ • Mutual watchdog: daemon A watches B, B watches A                  │   │
│  │ • launchd persistence: auto-restart on kill/reboot                  │   │
│  │ • Process obfuscation: appears as system process                    │   │
│  │ • Bypass: Find and kill both + unload launchd (high friction)       │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ LAYER 6: FRICTION & CONFIG PROTECTION                      [PLANNED]│   │
│  │ • Motivational quote typing (anti-paste) to disable                 │   │
│  │ • Time delay (15 min cooling off)                                   │   │
│  │ • Rate limiting (once per week disable)                             │   │
│  │ • Encrypted config (can't just edit text file)                      │   │
│  │ • Bypass: Wait + type quote + limited window                        │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Layer Details

### Layer 1: External DNS (NextDNS) ✓ IMPLEMENTED

**Status**: Complete
**Documentation**: `next_dns.md`

```
Account:        frankbluemind.focusd@outlook.com
Profile ID:     395ea7
DNS Servers:    45.90.28.102 / 45.90.30.102
```

**How it works:**
- Router (Eero) configured with NextDNS servers
- All devices on network use NextDNS
- Mac agent (`brew install nextdns`) keeps linked IP updated
- Denylist blocks gaming domains

**Bypass difficulty**: Must log into NextDNS dashboard (external system)

---

### Layer 2: Local DNS (/etc/hosts)

**Status**: Planned
**Purpose**: Backup DNS blocking if NextDNS is bypassed

**Implementation:**
```
/etc/hosts entries:
0.0.0.0 steampowered.com
0.0.0.0 steamcommunity.com
0.0.0.0 store.steampowered.com
... (all Steam domains)
```

**Daemon behavior:**
- Monitors /etc/hosts for changes
- Auto-restores blocked entries if removed
- Runs as protected system service

---

### Layer 3: Network Firewall (macOS pf)

**Status**: Planned
**Purpose**: Block at IP level (DNS bypass protection)

**Implementation:**
```
# /etc/pf.conf additions
# Block Valve/Steam IP ranges
block drop out quick proto tcp from any to 208.64.200.0/22
block drop out quick proto tcp from any to 208.78.164.0/22
block drop out quick proto tcp from any to 205.196.6.0/24
```

**Why needed:**
- Steam could hardcode IPs, bypassing DNS
- VPN could use different DNS
- Direct IP access in hosts file

---

### Layer 4: Application Enforcement

**Status**: Planned
**Purpose**: Kill processes, delete files

**Components:**

**Process Monitor:**
```python
BLOCKED_PROCESSES = ['steam', 'dota2', 'Steam Helper', 'steamwebhelper']

while True:
    for proc in psutil.process_iter():
        if proc.name().lower() in BLOCKED_PROCESSES:
            proc.kill()
    sleep(1)
```

**File Watcher:**
```python
NUKE_PATHS = [
    '/Applications/Steam.app',
    '~/Library/Application Support/Steam',
    '~/Downloads/*steam*.dmg',
]

on_created(path):
    if matches_blocked(path):
        shutil.rmtree(path)
```

---

### Layer 5: Guardian System

**Status**: Planned
**Purpose**: Protect the protectors

**Architecture:**
```
┌─────────────────┐         ┌─────────────────┐
│   app_mon       │◄───────►│   guardian      │
│   daemon        │ watches │   daemon        │
│                 │         │                 │
│ (disguised as   │         │ (disguised as   │
│  "mds_stores")  │         │  "distnoted")   │
└────────┬────────┘         └────────┬────────┘
         │                           │
         └─────────┬─────────────────┘
                   ▼
          ┌───────────────┐
          │    launchd    │
          │  KeepAlive:   │
          │    true       │
          └───────────────┘
```

**Features:**
- Mutual monitoring (if A dies, B restarts it)
- launchd KeepAlive (survives reboot)
- Process name obfuscation
- No exposed PID in CLI output

---

### Layer 6: Friction & Config Protection

**Status**: Planned
**Purpose**: Psychological barriers + tamper protection

**Disable Flow:**
```
User: sudo am off
         │
         ▼
┌─────────────────────────────┐
│ Rate Limit Check            │
│ "Last disable: 3 days ago"  │
│ "Must wait 4 more days"     │
└─────────────────────────────┘
         │ (if allowed)
         ▼
┌─────────────────────────────┐
│ 15-minute Delay             │
│ "Urges fade in 10-20 min"   │
│ [=====>              ] 12:00│
└─────────────────────────────┘
         │ (after delay)
         ▼
┌─────────────────────────────┐
│ Type Motivational Quote     │
│ (anti-paste detection)      │
│ > The only way to d_        │
└─────────────────────────────┘
         │ (if correct)
         ▼
┌─────────────────────────────┐
│ DISABLED for 1 hour max     │
│ Auto-re-enables             │
└─────────────────────────────┘
```

**Config Protection:**
- Encrypted with machine-derived key
- Can't just `vim config.yml` and whitelist Steam
- Integrity checksums detect tampering

---

## Implementation Roadmap

### Phase 1: External DNS ✓ COMPLETE
- [x] NextDNS account setup
- [x] Router (Eero) configuration
- [x] Mac agent installation
- [x] iPhone verification
- [ ] Add Steam domains to denylist

### Phase 2: Local Enforcement (MVP)
- [ ] Create `app_mon` Python project structure
- [ ] Implement process monitor (kill Steam/Dota2)
- [ ] Implement file watcher (delete game files)
- [ ] Implement /etc/hosts management
- [ ] Basic CLI: `am on`, `am off`, `am status`

### Phase 3: Persistence & Protection
- [ ] launchd service installation
- [ ] Guardian daemon (mutual watchdog)
- [ ] Process name obfuscation
- [ ] Friction barriers (quote typing, delay)

### Phase 4: Hardening
- [ ] macOS pf firewall rules
- [ ] Encrypted configuration
- [ ] Rate limiting (once per week)
- [ ] Logging and accountability

### Phase 5: Future Enhancements
- [ ] Cloud config storage
- [ ] Accountability partner notifications
- [ ] YubiKey for config changes
- [ ] Mobile companion app

---

## Project Structure (Planned)

```
focusd/
├── app_mon/                    # Main app_mon tool
│   ├── app_mon.py              # CLI entry point
│   ├── daemon.py               # Main enforcement daemon
│   ├── guardian.py             # Guardian watchdog daemon
│   ├── process_monitor.py      # Kill blocked processes
│   ├── file_watcher.py         # Delete blocked files
│   ├── hosts_manager.py        # Manage /etc/hosts
│   ├── firewall.py             # macOS pf rules
│   ├── friction.py             # Quote barrier, delays
│   ├── config.py               # Encrypted config handling
│   ├── blocklist.yml           # Blocked apps/domains
│   ├── pyproject.toml          # Poetry dependencies
│   ├── install.sh              # Installer script
│   ├── README.md               # User documentation
│   └── DESIGN.md               # Technical design
├── artifacts/
│   └── managed_dns/
│       ├── next_dns.md         # NextDNS setup guide
│       └── HLD_app_mon.md      # This document
├── chrome/                     # Existing Chrome Focus tool
└── CLAUDE.md                   # Project guidance
```

---

## CLI Interface (Planned)

```bash
# Enable all protection
sudo am on

# Disable (with friction)
sudo am off                    # Full friction: delay + quote + rate limit
sudo am off --duration 30      # Temporary disable, max 60 min

# Status
am status                      # Show all layers status
am status --verbose            # Detailed status

# Manage blocklist
am list                        # Show blocked apps/domains
am add steam                   # Add app to blocklist (easy)
am remove steam                # Remove from blocklist (hard - friction)

# Logs
am logs                        # Show recent block events
am logs --export              # Export for accountability
```

---

## Technology Stack

| Component | Technology |
|-----------|------------|
| Language | Python 3.8+ |
| Package Manager | Poetry |
| CLI Framework | Click |
| Process Monitoring | psutil |
| File Watching | watchdog |
| DNS Management | NextDNS API, /etc/hosts |
| Firewall | macOS pf |
| Persistence | launchd |
| Process Obfuscation | setproctitle |
| Config Encryption | cryptography (Fernet) |
| HTTP Requests | requests |

---

## Security Model

**Threat**: User with admin access trying to bypass protection

**Defense strategy**: Not absolute prevention, but MAXIMUM FRICTION

| Attack Vector | Defense |
|---------------|---------|
| Kill daemon | Guardian restarts it in 5s |
| Kill both daemons | launchd restarts in 30s |
| Unload launchd | Requires CLI which has friction |
| Edit /etc/hosts | Daemon auto-restores |
| Edit config | Encrypted, checksum verified |
| Use VPN for DNS | pf blocks IP ranges |
| Download Steam | File watcher deletes |
| Run Steam | Process monitor kills |
| Disable NextDNS | Layers 2-6 still active |

**Result**: Bypassing requires 30+ minutes of focused technical work. Urge fades by then.

---

## References

- Chrome Focus (existing tool): `/focusd/chrome/`
- NextDNS API: https://nextdns.github.io/api/
- Freedom App: https://freedom.to (inspiration)
- macOS pf manual: `man pf.conf`
- Steam domains: https://github.com/nickspaargaren/no-google (domain lists)
