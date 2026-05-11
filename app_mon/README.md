# App Mon

[![Build Status](https://github.com/eliteGoblin/focusd/actions/workflows/app_mon.yml/badge.svg)](https://github.com/eliteGoblin/focusd/actions/workflows/app_mon.yml)
[![codecov](https://codecov.io/gh/eliteGoblin/focusd/branch/master/graph/badge.svg)](https://codecov.io/gh/eliteGoblin/focusd)
[![Go Report Card](https://goreportcard.com/badge/github.com/eliteGoblin/focusd/app_mon)](https://goreportcard.com/report/github.com/eliteGoblin/focusd/app_mon)
[![Go Version](https://img.shields.io/github/go-mod/go-version/eliteGoblin/focusd?filename=app_mon%2Fgo.mod)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A self-enforcing application monitor that blocks distracting apps like Steam and Dota 2 on macOS.

## Features

- **Process Killing**: Automatically kills Steam/Dota2 processes when detected
- **File Deletion**: Removes Steam app bundle and Dota2 game files
- **Mutual Daemon Protection**: Two daemons monitor each other — kill one, the other restarts it
- **Auto-Start + 5-min Cron Respawn**: Plist `RunAtLoad` + `StartInterval=300` so launchd revives both daemons within 5 minutes even if simultaneously killed
- **Binary Relocation (killall-resistant)**: Each daemon spawn copies/hard-links the binary to a randomized system-looking basename and execs from there, so `killall appmon` and `pkill -f appmon` match nothing
- **Login-Items Obfuscation**: The LaunchAgent plist points at a relocated "launch stub" — macOS Login Items shows e.g. `com.apple.security.agent.f7cf9323`, not `appmon`
- **Encrypted Registry (source of truth)**: SQLCipher database holds daemon PIDs, plist label, launch stub path, version. Update flow and watcher both read from it — no split-brain
- **Orphan Reaper**: Watcher periodically scans the relocator cache dir and kills any process whose PID isn't in the registry (handles failed updates, racing spawns, stale state)
- **No Stop Command**: Intentional friction to prevent impulsive disabling

## Installation

```bash
cd app_mon
make deps
make build
make install
```

## Usage

```bash
# Start protection (auto-installs LaunchAgent)
appmon start

# Check status
appmon status

# List blocked applications
appmon list
```

**Note**: There is intentionally no `stop` command. This is by design to create friction.

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  Layer 1: macOS launchd (cron-like respawn)                  │
│  - RunAtLoad on login                                        │
│  - StartInterval: 300s (re-fires every 5 min)                │
│  - KeepAlive: { Crashed: true, SuccessfulExit: false }       │
│  - ProgramArguments[0] = launch stub (obfuscated basename)   │
└──────────────────────────────────────────────────────────────┘
                            │
┌──────────────────────────────────────────────────────────────┐
│  Layer 2: Mutual Daemon Monitoring                           │
│  ┌────────────────┐          ┌────────────────┐              │
│  │    Watcher     │◄────────►│    Guardian    │              │
│  │ - kills procs  │ monitors │ - restarts     │              │
│  │ - deletes files│  each    │   watcher      │              │
│  └────────────────┘  other   └────────────────┘              │
│  Each spawn = fresh randomized basename via Relocator        │
└──────────────────────────────────────────────────────────────┘
                            │
┌──────────────────────────────────────────────────────────────┐
│  Layer 3: Binary Relocation                                  │
│  - Cache dir: ~/.cache/.com.apple.xpc.<host-hash>/           │
│  - Basenames: com.apple.{cfprefsd,metadata,security,...}.    │
│    {xpc,helper,agent,...}.<random-hex>                       │
│  - killall appmon / pkill -f appmon → no match               │
└──────────────────────────────────────────────────────────────┘
                            │
┌──────────────────────────────────────────────────────────────┐
│  Layer 4: Encrypted Registry (source of truth)               │
│  - SQLCipher DB at <DataDir>/registry.db                     │
│  - Tracks: WatcherPID, GuardianPID, plist label, stub path   │
│  - Updater + watcher both read from it (no split-brain)      │
│  - Orphan reaper kills cache-dir PIDs not in registry        │
└──────────────────────────────────────────────────────────────┘
```

## What Gets Blocked

### Steam
**Processes:**
- `Steam`, `steam_osx`, `steamwebhelper`, `Steam Helper`

**Paths:**
- `/Applications/Steam.app`
- `~/Library/Application Support/Steam`
- `~/Library/Caches/com.valvesoftware.steam`
- `/opt/homebrew/Caskroom/steam` (Homebrew install)

### Dota 2
**Processes:**
- `dota2`, `dota_osx64`, `Dota 2`

**Paths:**
- `~/Library/Application Support/Steam/steamapps/common/dota 2 beta`
- Workshop content, shader caches

## Scan Interval

The watcher scans every **5 minutes**, matching the LaunchAgent's `StartInterval`. So blocked apps get killed within 5 min whether the daemons stay healthy or get respawned by launchd.

## Resilience

| Scenario | What Happens |
|----------|--------------|
| `killall appmon` / `pkill -f appmon` | No match — daemons exec from a randomized basename in a path that doesn't contain "appmon" |
| Kill watcher (by PID) | Guardian restarts it within 30s, with a fresh randomized name |
| Kill guardian (by PID) | Watcher restarts it within 60s, with a fresh randomized name |
| Kill both | launchd re-fires `appmon start` within 5 min (`StartInterval`) and respawns both daemons |
| System restart | LaunchAgent/Daemon auto-starts at next login (user mode) or boot (system mode) |
| Delete registry DB | Daemons recreate it on next start (loses tracking — orphan reaper still works because cache dir is canonical) |
| Delete LaunchAgent plist | Watcher restores it within 60s, pointing to the launch stub |
| Delete main binary `/usr/local/bin/appmon` | Watcher restores from hidden backup or GitHub release |
| Stale daemons from failed update | Orphan reaper kills any cache-dir PID missing from the encrypted registry, within 60s |

## File Locations

System mode (LaunchDaemon, root):

| File | Purpose |
|------|---------|
| `/usr/local/bin/appmon` | Main binary |
| `/Library/LaunchDaemons/com.apple.xpc.launchd.helper.<hex>.plist` | LaunchDaemon config (randomized label) |
| `/var/lib/appmon/registry.db` | SQLCipher-encrypted registry (PIDs, secrets) |
| `/var/lib/appmon/.key` | Encryption key for registry |
| `~/.cache/.com.apple.xpc.<host-hash>/` | Relocated daemon binaries + launch stub |
| `~/.config/.com.apple.preferences.<hex>/` | Backup manifest (hidden) |
| `~/.config/.com.apple.helper.<hex>/` etc | Binary backups (multiple hidden locations) |
| `/var/tmp/appmon.log` | Daemon logs |

User mode (LaunchAgent, current user):

| File | Purpose |
|------|---------|
| `~/.local/bin/appmon` | Main binary |
| `~/Library/LaunchAgents/com.apple.xpc.launchd.helper.<hex>.plist` | LaunchAgent config |
| `~/.appmon/registry.db` | Encrypted registry |

Both modes use the same randomized basename schemes; all paths are derived from a hash of the hostname and persisted in the encrypted registry.

## Why No Uninstall?

**By design**, there is no uninstall command. This is intentional friction to prevent impulsive disabling during moments of weakness. The tool is designed to protect you from yourself.

If you absolutely need to remove it, you'll need to manually find and remove the components - but this friction is the point.

## Development

```bash
# Run without installing
make dev-start

# Run tests
make test

# Build release binary
make build-release
```

## Project Structure

```
app_mon/
├── cmd/appmon/main.go      # CLI entry point
├── internal/
│   ├── domain/             # Entities & interfaces
│   ├── policy/             # Steam/Dota2 blocking rules
│   ├── daemon/             # Watcher & Guardian daemons
│   ├── infra/              # Process/file/registry implementations
│   └── usecase/            # Business logic
├── test/                   # Integration tests
├── Makefile
└── README.md
```

## Future Enhancements

- [ ] Friction barrier (type quote to disable)
- [ ] Cloud-synced configuration
- [ ] Web dashboard
- [ ] Windows support
