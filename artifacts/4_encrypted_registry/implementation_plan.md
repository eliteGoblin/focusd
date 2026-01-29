# Phase 1: Encrypted Registry - Implementation Plan

**GitHub Issue**: https://github.com/eliteGoblin/focusd/issues/9
**Requirements**: `requirements/app_mon/4_encrypted_registry_server_sync.md`

---

## Executive Summary

**Verdict**: SQLCipher makes sense for appmon, but the requirements need refinement to avoid over-engineering Phase 1.

**Key Recommendations**:
1. Separate **runtime state** (PIDs, heartbeat) from **persistent secrets** (process names, plist names)
2. Clear folder strategy for user/system modes
3. Minimal schema - only what's strictly necessary

---

## Analysis of Current State

### Current Registry Data (`internal/infra/registry.go`)
```go
type RegistryEntry struct {
    Version       int    // schema version
    WatcherPID    int    // runtime - changes on restart
    WatcherName   string // currently regenerated each start
    GuardianPID   int    // runtime - changes on restart
    GuardianName  string // currently regenerated each start
    LastHeartbeat int64  // runtime - updates every few seconds
    Mode          string // "user" or "system"
    AppVersion    string // version of running daemons
}
```

**Location**: `/var/tmp/.cf_sys_registry_<hash>` (MD5 of hostname)

### Problem Analysis

| Data | Type | Needs Encryption? | Rationale |
|------|------|-------------------|-----------|
| PIDs | Runtime | No | Changes on crash/restart, visible via `ps aux` anyway |
| Heartbeat | Runtime | No | Ephemeral, no security value |
| Process names | Persistent secret | **Yes** | Once generated, should persist across restarts |
| Plist name | Persistent secret | **Yes** | Hiding makes bypass harder |
| Mode/Version | Config | Maybe | Low value, but no harm |
| Backup paths | Persistent secret | **Yes** | Don't want user to easily find backups |

**Key Insight**: Current design regenerates process names on each start. The requirements want to change this to **generate once, persist forever** - this is the real value-add.

---

## Design Decisions

### Q1: Where to put key and DB file for user mode vs root mode?

**Recommended approach**:

```
User mode (non-sudo):
~/.appmon/
├── registry.db          # Encrypted SQLite
└── .key                  # 256-bit key (0600)

System mode (sudo):
/var/lib/appmon/
├── registry.db          # Encrypted SQLite
└── .key                  # 256-bit key (0600, root-owned)
```

**Rationale**:
- User mode: `~/.appmon/` is standard for user-specific app data
- System mode: `/var/lib/appmon/` is standard for system service data
- Don't put key alongside binary - separation of concerns
- Each mode has its own isolated storage

**How does root know which user's home?**
- **Answer**: Root mode doesn't use user's home at all - it uses `/var/lib/appmon/`
- This is correct because system-mode daemons run as root, not as user
- If user runs `sudo appmon start`, daemons run as root and use system paths

### Q2: What if app crashes and PID changes? Is PID really useful?

**Answer**: PIDs are useful but ephemeral. Here's how recovery works:

**Current flow**:
1. Daemon starts → registers `(pid, process_name)` in registry
2. Partner reads partner's PID from registry
3. Partner checks `IsPartnerAlive(pid)` via `processManager.IsRunning(pid)`
4. If daemon crashes:
   - launchd restarts daemon with NEW PID
   - New daemon re-registers with new PID (overwrites old)
   - Partner reads fresh PID on next check

**Why PIDs are still useful**:
- Cross-daemon communication (watcher ↔ guardian monitor each other)
- CLI `status` command shows which PIDs are running
- `kill` signal for graceful shutdown

**PID recycling risk**:
OS can reuse a PID. If watcher dies with PID 1234, another process might get 1234.
Partner might think watcher is alive when it's actually a random process.

**Mitigation** (already in place):
```go
// internal/infra/process.go - IsRunning() checks process exists
// Combined with obfuscated name matching reduces false positives
```

**For Phase 1**: Current PID-based approach is sufficient. The race window is tiny (launchd restart is milliseconds), and PID recycling to same obfuscated name is astronomically unlikely.

### Q3: Schema migration approach?

**Recommended**: Use `golang-migrate` or embed migrations in binary.

```go
// Embed migrations in binary
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Schema version in meta table
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT);
INSERT INTO meta VALUES ('schema_version', '1');
```

**Migration on startup**:
1. Open DB
2. Check `schema_version`
3. Apply pending migrations
4. Update `schema_version`

---

## User Decisions (Confirmed)

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Encryption | **SQLCipher** | Phase 2 ready, industry standard |
| Process names | **Regenerate on restart** | Keep current behavior, harder to pattern-match |
| Plist name | **Randomize and persist** | Hide from LaunchAgents/LaunchDaemons folder |

---

## Final Implementation Plan

### What We're Building

1. **SQLCipher encrypted database** at `~/.appmon/registry.db` (user) or `/var/lib/appmon/registry.db` (system)
2. **File-based 256-bit key** at `.key` in same folder (0600 permissions)
3. **Randomized plist name** generated once on first install, stored in encrypted DB
4. **Process names** continue to regenerate on each daemon start (no change)
5. **Schema migration** support via embedded SQL files

### Files to Create/Modify

| File | Action | Description |
|------|--------|-------------|
| `internal/domain/repository.go` | Modify | Add `KeyProvider`, `SecretStore` interfaces |
| `internal/infra/key_provider.go` | Create | `FileKeyProvider` with key generation |
| `internal/infra/encrypted_registry.go` | Create | SQLCipher implementation of `DaemonRegistry` + `SecretStore` |
| `internal/infra/encrypted_registry_test.go` | Create | Unit tests with in-memory SQLCipher |
| `internal/infra/migrations/001_initial.sql` | Create | Initial schema |
| `internal/infra/launchd.go` | Modify | Read plist name from SecretStore |
| `cmd/appmon/main.go` | Modify | Initialize encrypted registry, generate plist name on first install |
| `go.mod` | Modify | Add `github.com/mutecomm/go-sqlcipher/v4` |

### Minimal Schema

```sql
-- migrations/001_initial.sql

-- Runtime daemon state (PIDs, heartbeat - updated frequently)
CREATE TABLE daemon_state (
    role TEXT PRIMARY KEY,           -- 'watcher' or 'guardian'
    pid INTEGER NOT NULL,
    process_name TEXT NOT NULL,
    last_heartbeat INTEGER NOT NULL,
    app_version TEXT
);

-- Persistent secrets (generated once on install)
CREATE TABLE secrets (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
-- Required keys: 'plist_name'

-- Schema metadata
CREATE TABLE meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
-- Keys: 'schema_version', 'install_mode', 'first_install_at'
```

### Implementation Steps

1. **Setup CGO + SQLCipher dependency**
   - Add to go.mod
   - Verify `CGO_ENABLED=1` builds work
   - Test on both arm64 and amd64

2. **Implement KeyProvider interface**
   - `FileKeyProvider.GetKey()` - read from file
   - `FileKeyProvider.StoreKey()` - write with 0600 permissions
   - Generate 32 random bytes, base64 encode

3. **Implement EncryptedRegistry**
   - Open SQLCipher with key from KeyProvider
   - Run migrations on first open
   - Implement `DaemonRegistry` interface (same as FileRegistry)
   - Add `SecretStore` methods: `GetSecret()`, `SetSecret()`

4. **Generate plist name on first install**
   - Check if `plist_name` secret exists
   - If not, generate: `com.apple.xpc.launchd.helper.<8-char-random>`
   - Store in secrets table

5. **Update launchd.go to use stored plist name**
   - Read from SecretStore instead of hardcoded constant
   - Update plist file path based on stored name

6. **Migration from old JSON registry**
   - On first run with new code, check for old JSON file
   - Import mode, app_version if present
   - Delete old JSON file

7. **Testing**
   - Unit tests with in-memory SQLCipher
   - Integration test: fresh install creates folder/key/db
   - Integration test: plist name persists across restarts
   - Manual test: `sqlite3 registry.db` fails without key

### Folder Structure After Implementation

```
User mode:
~/.appmon/
├── registry.db          # SQLCipher encrypted
└── .key                  # 256-bit AES key (0600)
~/.local/bin/appmon       # Binary (unchanged)
~/Library/LaunchAgents/com.apple.xpc.launchd.helper.a8f3b2c1.plist  # Randomized!

System mode:
/var/lib/appmon/
├── registry.db
└── .key                  # (0600, root-owned)
/usr/local/bin/appmon     # Binary (unchanged)
/Library/LaunchDaemons/com.apple.xpc.launchd.helper.a8f3b2c1.plist  # Randomized!
```

---

## Interface Design (Clean Architecture)

```go
// internal/domain/repository.go

// KeyProvider abstracts key source (file, keychain, server)
type KeyProvider interface {
    GetKey() ([]byte, error)
    StoreKey(key []byte) error
}

// SecretStore abstracts encrypted storage
type SecretStore interface {
    GetSecret(key string) (string, error)
    SetSecret(key, value string) error
    ListSecrets() (map[string]string, error)
}

// DaemonRegistry - existing interface, unchanged
type DaemonRegistry interface {
    Register(daemon Daemon) error
    GetPartner(role DaemonRole) (*Daemon, error)
    UpdateHeartbeat(role DaemonRole) error
    IsPartnerAlive(role DaemonRole) (bool, error)
    GetAll() (*RegistryEntry, error)
    Clear() error
    GetRegistryPath() string
}
```

**Implementation**:
```go
// internal/infra/key_provider.go
type FileKeyProvider struct { path string }
type KeychainKeyProvider struct { service string }  // Phase 1b

// internal/infra/encrypted_registry.go
type EncryptedRegistry struct {
    db         *sql.DB
    keyProvider KeyProvider
}
```

---

## Test Plan

| ID | Test | Expected Result |
|----|------|-----------------|
| TC1 | Fresh install (user mode) | Creates ~/.appmon/, .key (0600), registry.db |
| TC2 | Fresh install (system mode) | Creates /var/lib/appmon/, .key (0600), registry.db |
| TC3 | Key file permissions | `stat ~/.appmon/.key` shows 0600 |
| TC4 | DB unreadable without key | `sqlite3 ~/.appmon/registry.db .tables` fails |
| TC5 | DB readable with key | App can read/write daemon state |
| TC6 | Plist name generated | First install creates random plist name in secrets |
| TC7 | Plist name persists | Restart uses same plist name from DB |
| TC8 | Plist file uses random name | `ls ~/Library/LaunchAgents/` shows obfuscated name |
| TC9 | Schema migration | Future v2 migration applies cleanly |
| TC10 | Old JSON migration | Imports mode/version from old file, deletes it |

---

## Next Steps

1. ✅ Create GitHub issue for "Phase 1: Encrypted Registry with SQLCipher"
2. Implement in TDD order (interfaces → key provider → registry → integration)
3. Update CHANGELOG.md for v0.5.0
4. Tag and release
