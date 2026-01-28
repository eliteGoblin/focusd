# Feature Health Status System

**GitHub Issue**: [#5](https://github.com/eliteGoblin/focusd/issues/5)

## Overview

Provide visibility into whether core protection features are actually working, not just whether daemons are running.

## Problem Statement

Current `appmon status` only shows:
- Are daemons running? (yes/no)
- Last heartbeat time

This is insufficient for:
1. **Update rollback** - Need programmatic way to detect if update broke something
2. **User confidence** - Users can't verify system is truly protected
3. **Debugging** - Hard to diagnose partial failures

## User Story

As a user, I want to see the health status of each protection feature so I can verify my system is actually protected and diagnose issues.

## Features to Monitor

| Feature | Healthy | Degraded | Down |
|---------|---------|----------|------|
| **Self-protection** | Both daemons alive | One daemon alive | Both dead |
| **App blocking** | Enforcement running, recent scan | Running, no recent scan | Not running |
| **Binary backup** | All backups valid | Some backups corrupted | No valid backups |
| **Auto-start** | Plist installed + loaded | Plist exists, not loaded | Plist missing |
| **Freedom protection** | Freedom running, login item | Running, no login item | Not running |

## CLI Output

### Healthy System

```
$ appmon status

=== appmon Status ===
Version: 0.2.0
Mode: user

Feature Health:
  ✓ Self-protection     Both daemons running
  ✓ App blocking        Last scan: 30s ago
  ✓ Binary backup       3/3 backups valid
  ✓ Auto-start          LaunchAgent installed

Blocked applications:
  - Steam
  - Dota 2
=====================
```

### Degraded System

```
$ appmon status

=== appmon Status ===
Version: 0.2.0
Mode: user

Feature Health:
  ✓ Self-protection     Both daemons running
  ✓ App blocking        Last scan: 30s ago
  ⚠ Binary backup       1/3 backups valid (run 'appmon start' to repair)
  ✓ Auto-start          LaunchAgent installed

Blocked applications:
  - Steam
  - Dota 2
=====================
```

### Down System

```
$ appmon status

=== appmon Status ===
Version: 0.2.0
Status: NOT RUNNING

Feature Health:
  ✗ Self-protection     No daemons running
  ✗ App blocking        Enforcement stopped
  ✓ Binary backup       3/3 backups valid
  ⚠ Auto-start          Plist exists but not loaded

Run 'appmon start' to enable protection.
=====================
```

## Implementation

### Health State Enum

```go
// internal/domain/health.go
type HealthState string

const (
    HealthHealthy  HealthState = "healthy"
    HealthDegraded HealthState = "degraded"
    HealthDown     HealthState = "down"
)
```

### Health Status Structure

```go
type HealthStatus struct {
    Feature     string      // "self-protection", "app-blocking", etc.
    State       HealthState
    Message     string      // Human-readable description
    LastChecked time.Time
}
```

### Health Manager

```go
// internal/infra/health.go
type HealthManager struct {
    pm            ProcessManager
    registry      DaemonRegistry
    backupManager *BackupManager
    launchdMgr    *LaunchdManager
}

// Check all features, return slice of statuses
func (hm *HealthManager) CheckAll() []HealthStatus

// Quick check: is system in acceptable state?
// Returns true if no features are "down"
func (hm *HealthManager) IsSystemHealthy() bool

// Get status of specific feature
func (hm *HealthManager) CheckFeature(feature string) HealthStatus
```

### Feature Check Functions

```go
func (hm *HealthManager) checkSelfProtection() HealthStatus {
    entry, _ := hm.registry.GetAll()
    if entry == nil {
        return HealthStatus{Feature: "self-protection", State: HealthDown, Message: "No daemons registered"}
    }

    watcherAlive := hm.pm.IsRunning(entry.WatcherPID)
    guardianAlive := hm.pm.IsRunning(entry.GuardianPID)

    if watcherAlive && guardianAlive {
        return HealthStatus{Feature: "self-protection", State: HealthHealthy, Message: "Both daemons running"}
    }
    if watcherAlive || guardianAlive {
        return HealthStatus{Feature: "self-protection", State: HealthDegraded, Message: "One daemon running"}
    }
    return HealthStatus{Feature: "self-protection", State: HealthDown, Message: "No daemons running"}
}

func (hm *HealthManager) checkBinaryBackup() HealthStatus {
    config, err := hm.backupManager.GetConfig()
    if err != nil {
        return HealthStatus{Feature: "binary-backup", State: HealthDown, Message: "No backup config"}
    }

    validCount := 0
    for _, path := range config.BackupPaths {
        if sha, _ := computeSHA256(path); sha == config.SHA256 {
            validCount++
        }
    }

    total := len(config.BackupPaths)
    if validCount == total {
        return HealthStatus{Feature: "binary-backup", State: HealthHealthy,
            Message: fmt.Sprintf("%d/%d backups valid", validCount, total)}
    }
    if validCount > 0 {
        return HealthStatus{Feature: "binary-backup", State: HealthDegraded,
            Message: fmt.Sprintf("%d/%d backups valid", validCount, total)}
    }
    return HealthStatus{Feature: "binary-backup", State: HealthDown, Message: "No valid backups"}
}
```

## Programmatic API for Update Command

```go
// Used by update command for rollback decision
func (u *Updater) VerifyHealth(timeout time.Duration) error {
    healthMgr := infra.NewHealthManager(...)

    deadline := time.Now().Add(timeout)
    for time.Now().Before(deadline) {
        if healthMgr.IsSystemHealthy() {
            return nil
        }
        time.Sleep(time.Second)
    }

    // Collect failure details
    statuses := healthMgr.CheckAll()
    var failures []string
    for _, s := range statuses {
        if s.State == HealthDown {
            failures = append(failures, fmt.Sprintf("%s: %s", s.Feature, s.Message))
        }
    }
    return fmt.Errorf("health check failed: %s", strings.Join(failures, "; "))
}
```

## Definition of Done

- [ ] `appmon status` displays health of each feature
- [ ] Uses icons: ✓ (healthy), ⚠ (degraded), ✗ (down)
- [ ] Each feature has clear healthy/degraded/down criteria
- [ ] Health check runs on every `status` command
- [ ] Programmatic `HealthManager` API available
- [ ] `IsSystemHealthy()` method for update rollback
- [ ] Health checks complete in <100ms total
- [ ] Unit tests for each feature check
- [ ] Integration test verifying status output format

## Performance Requirements

- Total health check time: <100ms
- No network calls in health checks (GitHub version check is separate)
- File reads cached where possible

## Out of Scope

- Historical health metrics
- Alerting/notifications
- Remote health reporting
- Health check daemon (runs only on status command)

## Dependencies

- None (foundation for other features)

## Dependents

- [#4 appmon update command](https://github.com/eliteGoblin/focusd/issues/4) - uses for rollback
- [#6 Freedom App Protection](https://github.com/eliteGoblin/focusd/issues/6) - adds Freedom health
