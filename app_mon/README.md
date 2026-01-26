# App Mon

A self-enforcing application monitor that blocks distracting apps like Steam and Dota 2 on macOS.

## Features

- **Process Killing**: Automatically kills Steam/Dota2 processes when detected
- **File Deletion**: Removes Steam app bundle and Dota2 game files
- **Mutual Daemon Protection**: Two daemons monitor each other - kill one, the other restarts it
- **Auto-Start on Login**: Installs as a LaunchAgent for persistence across reboots
- **Obfuscated Process Names**: Daemons appear as system processes (e.g., `com.apple.cfprefsd.xpc.abc123`)
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
│  Layer 1: macOS LaunchAgent                                  │
│  - Auto-starts on login                                      │
│  - KeepAlive: restarts if crashed                            │
└──────────────────────────────────────────────────────────────┘
                            │
┌──────────────────────────────────────────────────────────────┐
│  Layer 2: Mutual Daemon Monitoring                           │
│  ┌────────────────┐          ┌────────────────┐             │
│  │    Watcher     │◄────────►│    Guardian    │             │
│  │ - kills procs  │ monitors │ - restarts     │             │
│  │ - deletes files│  each    │   watcher      │             │
│  └────────────────┘  other   └────────────────┘             │
└──────────────────────────────────────────────────────────────┘
                            │
┌──────────────────────────────────────────────────────────────┐
│  Layer 3: Obfuscated Process Names                           │
│  - Random system-looking names per restart                   │
│  - Example: com.apple.security.worker.789abc                 │
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

The daemon scans every **10 minutes** (configurable via code). This is a balance between responsiveness and CPU usage.

## Resilience

| Scenario | What Happens |
|----------|--------------|
| Kill watcher | Guardian restarts it within 30s |
| Kill guardian | Watcher restarts it within 60s |
| Kill both | macOS LaunchAgent restarts appmon |
| System restart | LaunchAgent auto-starts appmon |
| Delete registry file | Daemons recreate it on next heartbeat |
| Delete LaunchAgent plist | Watcher restores it automatically |

## File Locations

| File | Purpose |
|------|---------|
| `/usr/local/bin/appmon` | Main binary |
| `~/Library/LaunchAgents/com.focusd.appmon.plist` | LaunchAgent config |
| `/var/tmp/.cf_sys_registry_*` | Hidden daemon registry |
| `/var/tmp/appmon.log` | Daemon logs |

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
