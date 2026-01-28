# Changelog

All notable changes to appmon will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
