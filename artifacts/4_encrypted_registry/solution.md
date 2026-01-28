# Solution: Encrypted Registry

**Requirement**: [4_encrypted_registry_server_sync.md](../../requirements/app_mon/4_encrypted_registry_server_sync.md)

## Scope

This solution covers **Phase 1a (MVP)** only:
- File-based symmetric key
- SQLCipher encrypted SQLite
- Random plist/process names
- No server dependency

---

## Architecture

### Component Diagram

```
┌─────────────────────────────────────────────────────────────────────────┐
│                              appmon                                     │
│                                                                         │
│  ┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐      │
│  │   KeyManager    │───►│EncryptedRegistry│───►│  SecretManager  │      │
│  │                 │    │                 │    │                 │      │
│  │ • GetOrCreate   │    │ • Open(key)     │    │ • GenPlistName  │      │
│  │ • GetKeyPath    │    │ • GetDaemons    │    │ • GenProcPattern│      │
│  │                 │    │ • SetDaemons    │    │ • GetSecrets    │      │
│  └────────┬────────┘    │ • GetSecrets    │    └─────────────────┘      │
│           │             │ • SetSecrets    │                             │
│           │             │ • GetConfig     │                             │
│           │             │ • Migrate       │                             │
│           │             └────────┬────────┘                             │
│           │                      │                                      │
└───────────┼──────────────────────┼──────────────────────────────────────┘
            │                      │
            ▼                      ▼
     ┌──────────────┐      ┌──────────────────┐
     │ ~/.appmon/   │      │ ~/.appmon/       │
     │   .key       │      │   registry.db    │
     │ (plaintext)  │      │ (encrypted)      │
     └──────────────┘      └──────────────────┘
```

### Folder Structure

```
User Mode (~/.appmon/):
├── .key                    # 256-bit symmetric key (base64)
├── registry.db             # SQLCipher encrypted SQLite
└── backups/                # Binary backups (existing, moved here)
    ├── appmon.bak1
    └── appmon.bak2

System Mode (/var/lib/appmon/):
├── .key
├── registry.db
└── backups/
    ├── appmon.bak1
    └── appmon.bak2

Binaries (unchanged):
├── ~/.local/bin/appmon     # User mode
└── /usr/local/bin/appmon   # System mode
```

---

## Data Model

### SQLite Schema

```sql
-- Schema version for migrations
CREATE TABLE schema_version (
    version INTEGER PRIMARY KEY
);

-- Device identity
CREATE TABLE device (
    id TEXT PRIMARY KEY,          -- UUID generated on install
    created_at INTEGER NOT NULL,  -- Unix timestamp
    exec_mode TEXT NOT NULL       -- 'user' or 'system'
);

-- Runtime secrets (randomized per install)
CREATE TABLE secrets (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
-- Keys:
--   'plist_name' -> 'com.apple.xpc.helper.a1b2c3'
--   'process_pattern_watcher' -> 'kernelmanagerd'
--   'process_pattern_guardian' -> 'diskarbitrationd'

-- Daemon state (replaces JSON registry)
CREATE TABLE daemons (
    role TEXT PRIMARY KEY,        -- 'watcher' or 'guardian'
    pid INTEGER NOT NULL,
    obfuscated_name TEXT,
    started_at INTEGER NOT NULL,
    last_heartbeat INTEGER
);

-- Backup locations
CREATE TABLE backups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL,
    checksum TEXT,                -- SHA256 for integrity
    created_at INTEGER NOT NULL,
    verified_at INTEGER
);

-- Config cache (for Phase 2, but schema ready)
CREATE TABLE config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    source TEXT DEFAULT 'local'   -- 'local' or 'server'
);
```

---

## Key Components

### 1. KeyManager

```go
// internal/infra/keymanager.go

package infra

import (
    "crypto/rand"
    "encoding/base64"
    "os"
    "path/filepath"
)

const keySize = 32 // 256 bits

type KeyManager struct {
    execMode *ExecModeConfig
}

func NewKeyManager(execMode *ExecModeConfig) *KeyManager

// GetOrCreateKey returns the encryption key, creating if needed
func (k *KeyManager) GetOrCreateKey() ([]byte, error) {
    keyPath := k.getKeyPath()

    // Try to read existing key
    if data, err := os.ReadFile(keyPath); err == nil {
        return base64.StdEncoding.DecodeString(string(data))
    }

    // Generate new key
    key := make([]byte, keySize)
    if _, err := rand.Read(key); err != nil {
        return nil, err
    }

    // Ensure directory exists
    if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
        return nil, err
    }

    // Write with restricted permissions
    encoded := base64.StdEncoding.EncodeToString(key)
    if err := os.WriteFile(keyPath, []byte(encoded), 0600); err != nil {
        return nil, err
    }

    return key, nil
}

func (k *KeyManager) getKeyPath() string {
    return filepath.Join(k.getDataDir(), ".key")
}

func (k *KeyManager) getDataDir() string {
    if k.execMode.Mode == ExecModeSystem {
        return "/var/lib/appmon"
    }
    home, _ := os.UserHomeDir()
    return filepath.Join(home, ".appmon")
}
```

### 2. EncryptedRegistry

```go
// internal/infra/encrypted_registry.go

package infra

import (
    "database/sql"
    "fmt"

    _ "github.com/mutecomm/go-sqlcipher/v4"
)

type EncryptedRegistry struct {
    db       *sql.DB
    keyMgr   *KeyManager
    dataDir  string
}

func NewEncryptedRegistry(execMode *ExecModeConfig) (*EncryptedRegistry, error) {
    keyMgr := NewKeyManager(execMode)
    key, err := keyMgr.GetOrCreateKey()
    if err != nil {
        return nil, fmt.Errorf("failed to get encryption key: %w", err)
    }

    dataDir := keyMgr.getDataDir()
    dbPath := filepath.Join(dataDir, "registry.db")

    // Open with encryption key
    dsn := fmt.Sprintf("%s?_pragma_key=x'%x'", dbPath, key)
    db, err := sql.Open("sqlite3", dsn)
    if err != nil {
        return nil, err
    }

    reg := &EncryptedRegistry{db: db, keyMgr: keyMgr, dataDir: dataDir}

    // Initialize schema
    if err := reg.initSchema(); err != nil {
        return nil, err
    }

    return reg, nil
}

func (r *EncryptedRegistry) initSchema() error {
    // Create tables if not exist
    // See schema above
}

// Daemon state methods (replaces FileRegistry)
func (r *EncryptedRegistry) GetDaemonEntry() (*DaemonRegistryEntry, error)
func (r *EncryptedRegistry) SetDaemonEntry(entry *DaemonRegistryEntry) error
func (r *EncryptedRegistry) UpdateHeartbeat(role DaemonRole) error
func (r *EncryptedRegistry) Clear() error

// Secret methods
func (r *EncryptedRegistry) GetSecret(key string) (string, error)
func (r *EncryptedRegistry) SetSecret(key, value string) error

// Close database
func (r *EncryptedRegistry) Close() error
```

### 3. SecretManager

```go
// internal/infra/secret_manager.go

package infra

import (
    "crypto/rand"
    "fmt"
)

// System-like plist name patterns
var plistPatterns = []string{
    "com.apple.xpc.launchd.helper.%s",
    "com.apple.security.agent.%s",
    "com.apple.coreservices.%s",
}

// System-like process name patterns
var processPatterns = []string{
    "kernelmanagerd",
    "diskarbitrationd",
    "syspolicyd",
    "trustd",
    "securityd",
}

type SecretManager struct {
    registry *EncryptedRegistry
}

func NewSecretManager(registry *EncryptedRegistry) *SecretManager

// GetOrCreateSecrets returns secrets, generating on first call
func (s *SecretManager) GetOrCreateSecrets() (*Secrets, error) {
    // Try to load existing
    secrets, err := s.loadSecrets()
    if err == nil {
        return secrets, nil
    }

    // Generate new secrets
    secrets = &Secrets{
        PlistName:          s.generatePlistName(),
        ProcessPatternWatcher:  s.pickProcessPattern(),
        ProcessPatternGuardian: s.pickProcessPattern(),
    }

    // Persist
    if err := s.saveSecrets(secrets); err != nil {
        return nil, err
    }

    return secrets, nil
}

func (s *SecretManager) generatePlistName() string {
    pattern := plistPatterns[randInt(len(plistPatterns))]
    suffix := randomHex(6)
    return fmt.Sprintf(pattern, suffix)
}
```

---

## Migration Strategy

### From JSON Registry to Encrypted SQLite

```go
// internal/infra/migration.go

func MigrateFromJSONRegistry(oldRegistry *FileRegistry, newRegistry *EncryptedRegistry) error {
    // 1. Read old JSON registry
    entry, err := oldRegistry.GetAll()
    if err != nil {
        return nil // No old registry, fresh install
    }

    // 2. Copy daemon state to new registry
    if err := newRegistry.SetDaemonEntry(entry); err != nil {
        return err
    }

    // 3. Delete old JSON file
    oldPath := oldRegistry.GetRegistryPath()
    if err := os.Remove(oldPath); err != nil {
        log.Printf("Warning: could not delete old registry: %v", err)
    }

    // 4. Log migration
    log.Println("Migrated to encrypted registry")

    return nil
}
```

### Startup Flow

```
┌─────────────────────────────────────────────────────────────┐
│                        appmon start                         │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  1. Detect exec mode (user vs system)                       │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  2. Initialize KeyManager                                   │
│     • Read or generate ~/.appmon/.key                       │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  3. Initialize EncryptedRegistry                            │
│     • Open SQLCipher with key                               │
│     • Create schema if needed                               │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  4. Check for old JSON registry                             │
│     • If exists: migrate data, delete old file              │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  5. Initialize SecretManager                                │
│     • Load or generate plist name, process patterns         │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  6. Continue with normal startup                            │
│     • Use secrets for plist installation                    │
│     • Use secrets for daemon process names                  │
└─────────────────────────────────────────────────────────────┘
```

---

## Security Analysis

### What's Protected

| Asset | Protection | Attack Vector | Mitigation |
|-------|------------|---------------|------------|
| Daemon PIDs | Encrypted in SQLite | Read registry file | Can't decrypt without key |
| Plist name | Encrypted in SQLite | Find plist to unload | Random name, not in registry |
| Process patterns | Encrypted in SQLite | Find process to kill | Random name, looks like system |
| Backup paths | Encrypted in SQLite | Delete backups | Paths not visible |

### What's NOT Protected (Accepted Risks)

| Risk | Reason Accepted |
|------|-----------------|
| Key file readable by user | User owns the machine; Phase 2 fixes with server |
| Root can read everything | Root access = game over anyway |
| Memory dump can reveal key | Sophisticated attack beyond threat model |
| Binary reverse engineering | Sophisticated attack beyond threat model |

### Friction Added

1. **Find registry** → Hidden folder (~/.appmon/)
2. **Read registry** → Encrypted (SQLCipher)
3. **Get key** → Hidden file (.key)
4. **Find plist** → Random name, looks like system
5. **Find process** → Random name, looks like system

Each layer adds time = more time for urge to pass.

---

## Dependencies

### New Go Dependencies

```go
// go.mod additions
require (
    github.com/mutecomm/go-sqlcipher/v4 v4.4.2  // SQLCipher bindings
)
```

### Build Requirements

SQLCipher requires CGO and OpenSSL:
```bash
# macOS
brew install openssl

# Build command
CGO_ENABLED=1 go build -o appmon ./cmd/appmon
```

---

## Future Considerations (Phase 2)

When adding server sync:

1. **Key source changes**: Server generates key, client downloads
2. **Schema additions**: `sync_status`, `server_id` columns
3. **New tables**: `auth` for token storage
4. **Migration**: Replace file-based key with server-generated

The SQLite schema is designed to be additive - Phase 2 adds columns/tables, doesn't change existing.
