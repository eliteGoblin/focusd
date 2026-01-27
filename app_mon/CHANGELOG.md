# Changelog

All notable changes to appmon will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
