# Implementation Plan: Encrypted Registry (Phase 1a)

**Solution**: [solution.md](./solution.md)
**Requirement**: [4_encrypted_registry_server_sync.md](../../requirements/app_mon/4_encrypted_registry_server_sync.md)

---

## Overview

| Metric | Estimate |
|--------|----------|
| Total tasks | 12 |
| New files | 4 |
| Modified files | 6 |
| Dependencies | 1 (go-sqlcipher) |

---

## Task Breakdown

### Task 1: Add SQLCipher Dependency

**Files**: `go.mod`, `go.sum`

```bash
go get github.com/mutecomm/go-sqlcipher/v4
```

**Verification**:
```go
import _ "github.com/mutecomm/go-sqlcipher/v4"
// Build succeeds with CGO_ENABLED=1
```

**Notes**:
- Requires CGO enabled
- May need `brew install openssl` on macOS
- Update Makefile if needed for CGO

---

### Task 2: Implement KeyManager

**New file**: `internal/infra/keymanager.go`

**Interface**:
```go
type KeyManager struct {
    execMode *ExecModeConfig
}

func NewKeyManager(execMode *ExecModeConfig) *KeyManager
func (k *KeyManager) GetOrCreateKey() ([]byte, error)
func (k *KeyManager) GetDataDir() string
func (k *KeyManager) GetKeyPath() string
```

**Implementation details**:
- Generate 256-bit random key using `crypto/rand`
- Store as base64 encoded string
- Create directory with 0700 permissions
- Create key file with 0600 permissions
- Handle both user mode (~/.appmon/) and system mode (/var/lib/appmon/)

**Test**: Unit test for key generation, file permissions

---

### Task 3: Implement EncryptedRegistry Core

**New file**: `internal/infra/encrypted_registry.go`

**Interface**:
```go
type EncryptedRegistry struct {
    db      *sql.DB
    dataDir string
}

func NewEncryptedRegistry(execMode *ExecModeConfig) (*EncryptedRegistry, error)
func (r *EncryptedRegistry) Close() error
func (r *EncryptedRegistry) initSchema() error
```

**Implementation details**:
- Use KeyManager to get encryption key
- Open SQLCipher database with key
- Create all tables on first open
- Store schema version for future migrations

**Test**: Unit test for database creation, schema initialization

---

### Task 4: Implement DaemonRegistry Interface

**File**: `internal/infra/encrypted_registry.go` (add methods)

**Interface** (compatible with existing FileRegistry):
```go
func (r *EncryptedRegistry) GetAll() (*DaemonRegistryEntry, error)
func (r *EncryptedRegistry) Register(entry *DaemonRegistryEntry) error
func (r *EncryptedRegistry) UpdateHeartbeat(role string) error
func (r *EncryptedRegistry) Clear() error
func (r *EncryptedRegistry) GetRegistryPath() string
```

**Implementation details**:
- Same interface as FileRegistry for drop-in replacement
- Store daemon data in `daemons` table
- Convert between Go structs and SQL rows

**Test**: Unit test matching existing FileRegistry tests

---

### Task 5: Implement SecretManager

**New file**: `internal/infra/secret_manager.go`

**Interface**:
```go
type Secrets struct {
    PlistName              string
    ProcessPatternWatcher  string
    ProcessPatternGuardian string
}

type SecretManager struct {
    registry *EncryptedRegistry
}

func NewSecretManager(registry *EncryptedRegistry) *SecretManager
func (s *SecretManager) GetOrCreateSecrets() (*Secrets, error)
```

**Implementation details**:
- Generate system-like plist names (com.apple.xpc.*, etc.)
- Pick system-like process names (kernelmanagerd, etc.)
- Store in `secrets` table
- Return cached secrets on subsequent calls

**Test**: Unit test for secret generation, persistence

---

### Task 6: Implement Migration

**New file**: `internal/infra/migration.go`

**Interface**:
```go
func MigrateFromJSONRegistry(oldPath string, newRegistry *EncryptedRegistry) error
func NeedsMigration(dataDir string) bool
```

**Implementation details**:
- Check if old JSON registry exists
- Read daemon state from JSON
- Write to encrypted registry
- Delete old JSON file
- Log migration status

**Test**: Integration test with sample JSON registry

---

### Task 7: Update ExecModeConfig

**File**: `internal/infra/execmode.go`

**Changes**:
- Add `DataDir` field to ExecModeConfig
- Update `DetectExecMode()` to set DataDir
- User mode: `~/.appmon`
- System mode: `/var/lib/appmon`

```go
type ExecModeConfig struct {
    Mode       ExecMode
    BinaryPath string
    PlistDir   string
    PlistPath  string  // Will become dynamic based on secrets
    DataDir    string  // NEW: ~/.appmon or /var/lib/appmon
    IsRoot     bool
}
```

---

### Task 8: Update LaunchdManager for Dynamic Plist Name

**File**: `internal/infra/launchd.go`

**Changes**:
- Accept plist name from SecretManager instead of constant
- Update `generatePlistContent()` to use secret plist name
- Update `Install()`, `NeedsUpdate()`, `CleanupOtherMode()`

```go
func NewLaunchdManager(execMode *ExecModeConfig, secrets *Secrets) *LaunchdManager
```

---

### Task 9: Update Obfuscator for Secret Process Names

**File**: `internal/infra/obfuscator.go`

**Changes**:
- Accept process patterns from SecretManager
- Generate names matching the secret pattern
- E.g., if pattern is "kernelmanagerd", generate "kernelmanagerd-a1b2c3"

```go
func NewObfuscator(secrets *Secrets) *Obfuscator
func (o *Obfuscator) GenerateName(role DaemonRole) string
```

---

### Task 10: Update Bootstrap for New Registry

**File**: `internal/daemon/bootstrap.go`

**Changes**:
- Use EncryptedRegistry instead of FileRegistry
- Use SecretManager for process names
- Pass secrets to Obfuscator

---

### Task 11: Update main.go Startup Flow

**File**: `cmd/appmon/main.go`

**Changes in `runStart()`**:
```go
// OLD:
registry := infra.NewFileRegistry(pm)

// NEW:
encRegistry, err := infra.NewEncryptedRegistry(execMode)
if err != nil {
    return fmt.Errorf("failed to initialize registry: %w", err)
}
defer encRegistry.Close()

// Check for migration
if infra.NeedsMigration(execMode.DataDir) {
    if err := infra.MigrateFromJSONRegistry(oldRegistryPath, encRegistry); err != nil {
        log.Printf("Warning: migration failed: %v", err)
    }
}

// Get secrets
secretMgr := infra.NewSecretManager(encRegistry)
secrets, err := secretMgr.GetOrCreateSecrets()
if err != nil {
    return fmt.Errorf("failed to get secrets: %w", err)
}

// Use secrets for launchd
launchdManager := infra.NewLaunchdManager(execMode, secrets)
```

---

### Task 12: Update Makefile for CGO

**File**: `Makefile`

**Changes**:
```makefile
# Enable CGO for SQLCipher
export CGO_ENABLED=1

build:
	CGO_ENABLED=1 go build -o build/appmon ./cmd/appmon

# Note: Cross-compilation may require additional setup
```

---

## Implementation Order

```
┌─────────────────────────────────────────────────────────────┐
│  Week 1: Foundation                                         │
├─────────────────────────────────────────────────────────────┤
│  1. Add SQLCipher dependency                                │
│  2. Implement KeyManager                                    │
│  3. Implement EncryptedRegistry Core                        │
│  4. Implement DaemonRegistry Interface                      │
│  5. Unit tests for above                                    │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  Week 2: Secrets & Migration                                │
├─────────────────────────────────────────────────────────────┤
│  6. Implement SecretManager                                 │
│  7. Implement Migration                                     │
│  8. Update ExecModeConfig                                   │
│  9. Unit tests for above                                    │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  Week 3: Integration                                        │
├─────────────────────────────────────────────────────────────┤
│  10. Update LaunchdManager                                  │
│  11. Update Obfuscator                                      │
│  12. Update Bootstrap                                       │
│  13. Update main.go                                         │
│  14. Update Makefile                                        │
│  15. Integration tests                                      │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  Week 4: E2E Testing & Polish                               │
├─────────────────────────────────────────────────────────────┤
│  16. E2E test: fresh install                                │
│  17. E2E test: migration from old version                   │
│  18. E2E test: user mode / system mode                      │
│  19. Fix bugs, edge cases                                   │
│  20. Documentation updates                                  │
└─────────────────────────────────────────────────────────────┘
```

---

## Risk Mitigation

| Risk | Mitigation |
|------|------------|
| SQLCipher build issues | Test on clean machine, document prerequisites |
| Migration data loss | Backup old registry before migration |
| Performance regression | Benchmark registry operations |
| Existing tests break | Update test fixtures, mock encrypted registry |

---

## Rollback Plan

If issues discovered after release:

1. Keep old FileRegistry code (don't delete)
2. Add feature flag: `--legacy-registry`
3. If critical bug: release patch reverting to FileRegistry
4. Fix issue, re-release with EncryptedRegistry

---

## Definition of Done

- [ ] All 12 tasks completed
- [ ] Unit tests passing
- [ ] Integration tests passing
- [ ] E2E tests passing
- [ ] User mode works
- [ ] System mode works
- [ ] Migration from old registry works
- [ ] CI/CD builds successfully (CGO)
- [ ] README updated with new prerequisites
