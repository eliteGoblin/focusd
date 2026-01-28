# appmon update Command

**GitHub Issue**: [#4](https://github.com/eliteGoblin/focusd/issues/4)

## Overview

Provide a safe self-update mechanism that downloads the latest version from GitHub, with automatic rollback if the update breaks functionality.

## Problem Statement

Users have no easy way to update appmon. Manual process requires:
1. Download from GitHub releases
2. Stop daemons
3. Replace binary
4. Update backups
5. Restart daemons

This friction leads to users running outdated versions.

## User Stories

1. As a user, I want to check if updates are available without installing
2. As a user, I want to update with a single command
3. As a user, I want automatic rollback if update breaks my protection

## CLI Interface

```bash
# Check for updates (no changes)
$ appmon update --check
Current version: 0.1.0
Latest version:  0.2.0
Update available!

# Perform update
$ appmon update
Current version: 0.1.0
Latest version:  0.2.0

Downloading v0.2.0...
Stopping daemons...
Backing up current binary...
Installing new binary...
Updating backup copies...
Restarting daemons...
Verifying health...

✓ Update successful!
  Version: 0.2.0
  All daemons running

# Already up to date
$ appmon update
Current version: 0.2.0
Latest version:  0.2.0
Already up to date.
```

## Update Flow

```
┌─────────────────────────────────────────────────────────────┐
│                      appmon update                          │
├─────────────────────────────────────────────────────────────┤
│  1. Get current version (from binary)                       │
│  2. Get latest version (GitHub API)                         │
│  3. Compare → if same, exit "Already up to date"            │
│  4. Download new binary to temp location                    │
│  5. Stop daemons gracefully (SIGTERM)                       │
│  6. Backup current binary to rollback location              │
│  7. Replace main binary (atomic write)                      │
│  8. Update all backup copies                                │
│  9. Start daemons                                           │
│ 10. Wait 10s, verify both daemons running                   │
│ 11. If healthy → success                                    │
│ 12. If unhealthy → rollback                                 │
└─────────────────────────────────────────────────────────────┘
```

## Rollback Flow

```
┌─────────────────────────────────────────────────────────────┐
│                       Rollback                              │
├─────────────────────────────────────────────────────────────┤
│  1. Stop any running daemons                                │
│  2. Restore main binary from rollback backup                │
│  3. Restore all backup copies from rollback                 │
│  4. Start daemons                                           │
│  5. Verify daemons running                                  │
│  6. Report: "Update failed, rolled back to vX.X.X"          │
└─────────────────────────────────────────────────────────────┘
```

## Rollback Criteria

Update is considered **failed** if after 10 seconds:
- Watcher daemon not running, OR
- Guardian daemon not running

## Existing Infrastructure

| Component | Location | Purpose |
|-----------|----------|---------|
| `GitHubDownloader` | `internal/infra/github_downloader.go` | Fetch releases, download binaries |
| `BackupManager` | `internal/infra/backup.go` | Manage backup copies |
| `FileRegistry` | `internal/infra/registry.go` | Track daemon PIDs |
| `ProcessManager` | `internal/infra/process.go` | Check if processes running |

## New Components

```go
// internal/infra/updater.go
type Updater struct {
    downloader    *GitHubDownloader
    backupManager *BackupManager
    registry      DaemonRegistry
    pm            ProcessManager
    execMode      *ExecModeConfig
}

// Check if update available
func (u *Updater) CheckUpdate() (current, latest string, available bool, err error)

// Perform the update with rollback support
func (u *Updater) PerformUpdate() error

// Rollback to previous version
func (u *Updater) Rollback(rollbackPath string) error

// Verify daemons are healthy after update
func (u *Updater) VerifyHealth(timeout time.Duration) error
```

## Edge Cases

| Scenario | Behavior |
|----------|----------|
| Already latest version | Print "Already up to date", exit 0 |
| No network connectivity | Error: "Failed to check for updates: network error" |
| GitHub API rate limited | Error: "GitHub API rate limited, try again later" |
| Download fails mid-way | Error, no changes made (download to temp first) |
| Daemon restart fails | Rollback, report failure with details |
| User mode updating system binary | Warn: "Cannot update system binary without sudo" |
| System mode updating user binary | Warn about mode mismatch |

## Definition of Done

- [ ] `appmon update` downloads and installs latest version
- [ ] `appmon update --check` shows update availability without installing
- [ ] Current binary backed up before replacement (for rollback)
- [ ] All backup copies updated to new version
- [ ] Daemons stopped before update, restarted after
- [ ] Automatic rollback if daemons fail to start within 10s
- [ ] Works for both user mode and system mode
- [ ] Clear progress messages during update
- [ ] Clear error messages on failure
- [ ] Unit tests for Updater logic
- [ ] Integration test for update flow (with mock GitHub)

## Out of Scope

- Scheduled auto-updates (cron/launchd timer)
- Update notifications / reminders
- Downgrade to specific version
- Update without restart (hot reload)

## Dependencies

- None (self-contained)

## Related

- [#5 Feature Health Status System](https://github.com/eliteGoblin/focusd/issues/5) - Will enhance rollback criteria
- [#6 Freedom App Protection](https://github.com/eliteGoblin/focusd/issues/6) - Depends on health system
