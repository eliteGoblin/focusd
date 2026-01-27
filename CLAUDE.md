# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Project Overview

**FocusD** is a multi-layer defense system against addictive apps and websites on macOS.

**Layers:**
1. **DNS Blocking (NextDNS)** - Network-level domain blocking for all devices
2. **App Blocking (appmon)** - Process killing and file deletion daemon

## Key Components

### app_mon (Active)
Go CLI daemon that blocks Steam/Dota2 by killing processes and deleting files.
- Location: `app_mon/`
- Docs: `app_mon/README.md`

### NextDNS Setup Guide
DNS-level blocking setup for router + all devices.
- Location: `artifacts/managed_dns/next_dns.md`

### Chrome Focus (Deprecated)
Chrome extension enforcer - moved to archive, no longer maintained.
- Location: `archive/chrome/`

## Project Structure

```
focusd/
├── app_mon/                    # App blocking daemon (Go)
│   ├── cmd/appmon/main.go      # CLI entry point
│   ├── internal/               # Domain, daemon, infra layers
│   └── README.md               # Documentation
├── artifacts/
│   └── managed_dns/
│       └── next_dns.md         # NextDNS setup guide
├── requirements/               # Feature specifications
├── archive/                    # Deprecated tools
│   └── chrome/                 # Chrome extension enforcer
└── CLAUDE.md                   # This file
```

## Technology Stack

**app_mon:**
- Language: Go 1.21+
- CLI: Cobra
- Logging: Zap
- Testing: testify + Ginkgo

## Common Commands

```bash
# Build app_mon
cd app_mon && make build

# Run app_mon
./build/appmon start
./build/appmon status
./build/appmon scan

# Run tests
make test
```

## Important Notes

- **No stop command**: Intentional friction to prevent impulsive disabling
- **Self-protection**: Dual daemon (watcher + guardian) monitor each other
- **Binary backups**: Auto-restore if deleted or corrupted
- **Version-aware restore**: Won't overwrite newer builds
