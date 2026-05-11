# Changelog

All notable changes to appmon will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.5.1] - 2026-05-12

### Features
- **killall / pkill resistance** via binary relocation. Each daemon spawn copies
  or hard-links `/usr/local/bin/appmon` to a randomized system-looking basename
  (`com.apple.cfprefsd.xpc.<hex>` etc) under an obfuscated cache dir
  (`~/.cache/.com.apple.xpc.<host-hash>/`) and execs from there. The kernel's
  `p_comm` for the running daemon is the relocated basename, so
  `killall appmon` matches nothing. Path does not contain "appmon", so
  `pkill -f appmon` also misses. Each spawn rotates the name, so an attacker
  who learns one name can only kill that one instance — peer-restart spawns
  the partner under a new random name.
- **Login Items obfuscation**: LaunchAgent / LaunchDaemon plist now references
  a "launch stub" — a relocated copy of the main binary stored at a
  randomized path. macOS Login Items shows the obfuscated basename instead
  of `appmon`.
- **5-min cron-like respawn**: plist `StartInterval: 300` plus
  `KeepAlive: { Crashed: true, SuccessfulExit: false }` means launchd
  re-fires `appmon start` every 5 minutes. `start` is idempotent — fast no-op
  if both daemons are alive, full respawn if dead. Belt-and-suspenders backup
  to peer-restart in case both daemons die simultaneously.
- **Daemon self-relocate fallback**: `appmon daemon …` re-execs itself from a
  relocated path on startup if exec'd from outside the relocator dir
  (e.g. by an older parent during an update). Idempotent.
- **Orphan reaper**: Watcher periodically (on startup + every 60s) scans
  the relocator cache dir and SIGKILLs any process whose PID is not in the
  encrypted registry. Makes the registry the single source of truth for
  daemon membership and self-heals after failed updates, racing spawns, or
  any stale state.

### Bug Fixes
- **CRITICAL — updater registry split-brain**: `NewUpdater` previously
  instantiated `NewFileRegistry` (the legacy JSON file at
  `/var/tmp/.cf_sys_registry_*`) while the live daemons write to the
  encrypted SQLCipher registry. Health checks therefore never saw the just-
  spawned daemons, every update timed out and triggered a phantom rollback,
  and each rollback spawned another pair of orphan daemons. Updater now
  opens the encrypted registry via `openUpdaterRegistry`, falling back to
  the legacy file registry only on hard failure.
- **Updater spawnDaemon bypasses relocation**: previously exec'd
  `/usr/local/bin/appmon` directly, forcing the daemon to self-relocate via
  `syscall.Exec`. Now relocates before exec like
  `daemon.StartDaemonWithMode` does; child runs from a randomized path
  immediately.
- **Health-check timeout 10s → 30s**: absorbs SQLCipher key derivation +
  daemon self-relocate on cold-start. Too-tight timeout was the proximate
  cause of phantom rollbacks (combined with the registry split-brain).

### Architecture
- New `infra.Relocator` (relocator.go) — copies/hard-links binaries to
  obfuscated paths, sweeps stale entries, lists PIDs running from the
  relocator dir.
- New `infra.EnsureLaunchStub` (launch_stub.go) — stable randomized stub for
  the LaunchAgent plist, persisted as a secret in the encrypted registry,
  auto-refreshed when content drifts from the main binary.
- Watcher gains `sweepStaleRelocations` + `reapOrphans` hooks on its 60s
  binary-check tick.

## [0.4.0] - 2026-01-29

### Features
- **Freedom app protection**: Monitor and protect Freedom.app from being killed or disabled
  - Auto-restart Freedom.app within 5 seconds if killed
  - Auto-restore Login Items if removed
  - Reports helper status (can't fix, but warns user)
  - Graceful skip when Freedom.app not installed
- **Freedom health in status**: `appmon status` now shows Freedom protection status
  - Shows app running, proxy active, helper running, login item present
  - Warns when helper is missing (reinstall Freedom to fix)

### Architecture
- **Testable design**: Extracted `CommandRunner` and `FileChecker` interfaces for dependency injection
- **~98% test coverage** for Freedom protection module
- **Nil-safe logging**: Helper methods prevent panic when logger is nil

### Bug Fixes
- **PID conversion bug**: Fixed `string(rune(pid))` → `strconv.Itoa(pid)` for correct PID string conversion
- **Log spam**: Changed helper-missing log from WARN to Debug (runs every 5 seconds)

## [0.3.0] - 2026-01-28

### Features
- **Self-update command**: `appmon update` downloads and installs the latest version from GitHub
  - Automatic rollback on failure (daemon startup failure, health check failure)
  - Creates rollback backup before update, restores on any error
  - Step-by-step progress output during update process
- **Local binary testing**: `appmon update --local-binary ./path/to/binary` for testing updates without GitHub
- **Idempotent start command**: `appmon start` now handles version comparison
  - Upgrade: Running older binary auto-updates and restarts daemons
  - Same version: Prints "already running, up to date"
  - Downgrade prevention: Refuses to downgrade running newer version
- **Daemon version tracking**: `appmon status` now shows both CLI and daemon versions
  - Warning when CLI version differs from running daemon version
- **Mode switching cleanup**: Automatically removes stale plist from other mode when switching (user↔system)

### Improvements
- Idempotent plist operations: `NeedsUpdate()`, `Update()`, `CleanupOtherMode()` methods
- Proper SUDO_USER handling: `getRealUserHome()` correctly resolves user's home under sudo
- PID > 0 guards in `VerifyDaemonsHealthy()` to prevent signaling PID 0
- Error propagation in `generatePlistContent()` for better debugging
- Proper `/dev/null` file descriptor handling for daemon spawning
- Removed sensitive paths (binary path, plist path) from status output

### Documentation
- Added health status system requirements (`requirements/app_mon/3_health_status_system.md`)
- Added encrypted registry & server sync requirements (`requirements/app_mon/4_encrypted_registry_server_sync.md`)
- Added implementation artifacts for encrypted registry feature

## [0.2.0] - 2026-01-28

### Features
- **Sudo/non-sudo auto-detection**: Automatically detects execution mode based on effective UID
  - `sudo appmon start` → LaunchDaemon (system-wide, /usr/local/bin)
  - `appmon start` → LaunchAgent (user-space, ~/.local/bin)
- **GitHub fallback restoration**: When local backups are corrupted/missing, automatically downloads latest release from GitHub
- **Execution mode configuration**: Separate paths for binary, plist, and backups based on mode

### Bug Fixes
- **Atomic binary writes**: Use temp file → sync → chmod → rename pattern to prevent corruption during copy
- **Timeout conflict**: Separate API timeout (30s) from download timeout (5min) to prevent large download failures
- **Daemon executable path**: Use installed binary path instead of `os.Executable()` for daemon spawning
- **Daemon mode detection**: Fix subprocess always using user mode regardless of actual execution context

### Improvements
- Remove unused `BackupDir` from `ExecModeConfig` (misleading API)
- Update launchd comment to reflect actual behavior (`launchctl load` vs bootstrap)
- Add regression tests for all bug fixes

### Documentation
- Add Freedom app implementation analysis (`artifacts/documents/topics/freedom_implementation_detail.md`)
- Add future enhancements roadmap (`requirements/app_mon/3_future_enhancements.md`)
- Update non-functional requirements for CI verification and code review process

## [0.1.0] - 2026-01-26

### Features
- Initial release
- Process killing for Steam and Dota 2
- File/directory deletion for blocked apps
- Mutual daemon protection (watcher ↔ guardian)
- LaunchAgent auto-start on login
- Obfuscated process names
- Binary self-backup and restoration
