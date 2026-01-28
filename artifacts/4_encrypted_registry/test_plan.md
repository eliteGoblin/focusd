# Test Plan: Encrypted Registry (Phase 1a)

**Solution**: [solution.md](./solution.md)
**Implementation**: [implementation_plan.md](./implementation_plan.md)

---

## Test Strategy

| Level | Scope | Tools |
|-------|-------|-------|
| Unit | Individual components | Go testing, mocks |
| Integration | Component interactions | Go testing, temp dirs |
| E2E | Full user scenarios | Shell scripts, manual |

---

## Unit Tests

### UT-1: KeyManager

**File**: `internal/infra/keymanager_test.go`

| Test Case | Description | Expected |
|-----------|-------------|----------|
| UT-1.1 | Generate key on first call | 32-byte key returned, file created |
| UT-1.2 | Return existing key | Same key returned, file unchanged |
| UT-1.3 | Key file permissions | File has 0600 permissions |
| UT-1.4 | Directory permissions | Dir has 0700 permissions |
| UT-1.5 | User mode path | ~/.appmon/.key |
| UT-1.6 | System mode path | /var/lib/appmon/.key |
| UT-1.7 | Invalid key file | Error returned, new key generated |

```go
func TestKeyManager_GetOrCreateKey_New(t *testing.T) {
    tmpDir := t.TempDir()
    execMode := &ExecModeConfig{Mode: ExecModeUser, DataDir: tmpDir}
    km := NewKeyManager(execMode)

    key, err := km.GetOrCreateKey()
    require.NoError(t, err)
    assert.Len(t, key, 32)

    // Verify file created
    keyPath := filepath.Join(tmpDir, ".key")
    info, err := os.Stat(keyPath)
    require.NoError(t, err)
    assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
```

---

### UT-2: EncryptedRegistry

**File**: `internal/infra/encrypted_registry_test.go`

| Test Case | Description | Expected |
|-----------|-------------|----------|
| UT-2.1 | Create new database | DB file created, schema initialized |
| UT-2.2 | Open existing database | Existing data accessible |
| UT-2.3 | Wrong key fails | Error opening with wrong key |
| UT-2.4 | Schema version set | schema_version table has version |
| UT-2.5 | Close database | No error, file flushed |

```go
func TestEncryptedRegistry_Create(t *testing.T) {
    tmpDir := t.TempDir()
    execMode := &ExecModeConfig{Mode: ExecModeUser, DataDir: tmpDir}

    reg, err := NewEncryptedRegistry(execMode)
    require.NoError(t, err)
    defer reg.Close()

    // Verify DB file created
    dbPath := filepath.Join(tmpDir, "registry.db")
    assert.FileExists(t, dbPath)
}

func TestEncryptedRegistry_WrongKey(t *testing.T) {
    tmpDir := t.TempDir()
    execMode := &ExecModeConfig{Mode: ExecModeUser, DataDir: tmpDir}

    // Create registry with key
    reg1, _ := NewEncryptedRegistry(execMode)
    reg1.Close()

    // Overwrite key file with different key
    keyPath := filepath.Join(tmpDir, ".key")
    os.WriteFile(keyPath, []byte("wrongkeywrongkeywrongkey12345678"), 0600)

    // Try to open - should fail
    _, err := NewEncryptedRegistry(execMode)
    assert.Error(t, err)
}
```

---

### UT-3: DaemonRegistry Interface

**File**: `internal/infra/encrypted_registry_test.go`

| Test Case | Description | Expected |
|-----------|-------------|----------|
| UT-3.1 | Register daemon | Entry stored in database |
| UT-3.2 | Get all daemons | Returns registered entry |
| UT-3.3 | Update heartbeat | Heartbeat timestamp updated |
| UT-3.4 | Clear registry | All entries deleted |
| UT-3.5 | Empty registry | Returns nil, no error |

```go
func TestEncryptedRegistry_DaemonOperations(t *testing.T) {
    reg := setupTestRegistry(t)
    defer reg.Close()

    entry := &DaemonRegistryEntry{
        WatcherPID:    1234,
        GuardianPID:   5678,
        LastHeartbeat: time.Now().Unix(),
        Mode:          "user",
    }

    // Register
    err := reg.Register(entry)
    require.NoError(t, err)

    // Get
    got, err := reg.GetAll()
    require.NoError(t, err)
    assert.Equal(t, entry.WatcherPID, got.WatcherPID)

    // Clear
    err = reg.Clear()
    require.NoError(t, err)

    got, _ = reg.GetAll()
    assert.Nil(t, got)
}
```

---

### UT-4: SecretManager

**File**: `internal/infra/secret_manager_test.go`

| Test Case | Description | Expected |
|-----------|-------------|----------|
| UT-4.1 | Generate secrets | Plist name and process patterns generated |
| UT-4.2 | Secrets persisted | Same secrets returned on subsequent calls |
| UT-4.3 | Plist name format | Matches com.apple.* pattern |
| UT-4.4 | Process names valid | Matches known system process patterns |
| UT-4.5 | Unique per install | Different installs get different secrets |

```go
func TestSecretManager_Generate(t *testing.T) {
    reg := setupTestRegistry(t)
    defer reg.Close()
    sm := NewSecretManager(reg)

    secrets, err := sm.GetOrCreateSecrets()
    require.NoError(t, err)

    // Plist name matches pattern
    assert.Regexp(t, `^com\.apple\.(xpc|security|coreservices)\.`, secrets.PlistName)

    // Process patterns are valid
    assert.NotEmpty(t, secrets.ProcessPatternWatcher)
    assert.NotEmpty(t, secrets.ProcessPatternGuardian)

    // Same on second call
    secrets2, _ := sm.GetOrCreateSecrets()
    assert.Equal(t, secrets.PlistName, secrets2.PlistName)
}
```

---

### UT-5: Migration

**File**: `internal/infra/migration_test.go`

| Test Case | Description | Expected |
|-----------|-------------|----------|
| UT-5.1 | Migrate valid JSON | Data copied to SQLite |
| UT-5.2 | No old registry | No error, no migration |
| UT-5.3 | Invalid JSON | Error logged, continues |
| UT-5.4 | Old file deleted | JSON file removed after migration |

```go
func TestMigration_Success(t *testing.T) {
    tmpDir := t.TempDir()

    // Create old JSON registry
    oldData := `{"watcher_pid":1234,"guardian_pid":5678}`
    oldPath := filepath.Join(tmpDir, "registry.json")
    os.WriteFile(oldPath, []byte(oldData), 0644)

    // Create new registry
    execMode := &ExecModeConfig{Mode: ExecModeUser, DataDir: tmpDir}
    reg, _ := NewEncryptedRegistry(execMode)
    defer reg.Close()

    // Migrate
    err := MigrateFromJSONRegistry(oldPath, reg)
    require.NoError(t, err)

    // Verify data migrated
    entry, _ := reg.GetAll()
    assert.Equal(t, 1234, entry.WatcherPID)

    // Verify old file deleted
    assert.NoFileExists(t, oldPath)
}
```

---

## Integration Tests

### IT-1: Full Startup Flow

**File**: `internal/infra/integration_test.go`

| Test Case | Description | Expected |
|-----------|-------------|----------|
| IT-1.1 | Fresh install | Key, DB, secrets created |
| IT-1.2 | Restart | Same secrets loaded |
| IT-1.3 | Migration then restart | Old data preserved |

```go
func TestIntegration_FreshInstall(t *testing.T) {
    tmpDir := t.TempDir()
    execMode := &ExecModeConfig{
        Mode:    ExecModeUser,
        DataDir: tmpDir,
    }

    // Simulate startup
    reg, err := NewEncryptedRegistry(execMode)
    require.NoError(t, err)
    defer reg.Close()

    sm := NewSecretManager(reg)
    secrets, err := sm.GetOrCreateSecrets()
    require.NoError(t, err)

    // Verify files created
    assert.FileExists(t, filepath.Join(tmpDir, ".key"))
    assert.FileExists(t, filepath.Join(tmpDir, "registry.db"))

    // Verify secrets valid
    assert.Contains(t, secrets.PlistName, "com.apple")
}
```

---

### IT-2: LaunchdManager with Secrets

**File**: `internal/infra/launchd_integration_test.go`

| Test Case | Description | Expected |
|-----------|-------------|----------|
| IT-2.1 | Install with secret plist name | Plist created with secret name |
| IT-2.2 | Plist content correct | Label matches secret name |
| IT-2.3 | Cleanup finds secret plist | Cleanup works with dynamic name |

---

### IT-3: Obfuscator with Secrets

| Test Case | Description | Expected |
|-----------|-------------|----------|
| IT-3.1 | Generate watcher name | Name based on secret pattern |
| IT-3.2 | Generate guardian name | Name based on secret pattern |
| IT-3.3 | Names are different | Watcher and guardian have different names |

---

## E2E Tests

### E2E-1: Fresh Install (User Mode)

**Script**: `test/e2e/fresh_install_user.sh`

```bash
#!/bin/bash
set -e

# Clean state
rm -rf ~/.appmon
rm -f ~/.local/bin/appmon

# Build
make build

# Install
./build/appmon start

# Verify files
test -f ~/.appmon/.key
test -f ~/.appmon/registry.db

# Verify permissions
stat -f "%Lp" ~/.appmon/.key | grep -q "600"

# Verify encrypted (file should not be readable as text)
! strings ~/.appmon/registry.db | grep -q "watcher"

# Verify daemons running
./build/appmon status | grep -q "RUNNING"

# Verify plist has random name
ls ~/Library/LaunchAgents/ | grep -q "com.apple"

echo "PASS: Fresh install user mode"
```

---

### E2E-2: Fresh Install (System Mode)

**Script**: `test/e2e/fresh_install_system.sh`

```bash
#!/bin/bash
set -e

# Requires sudo
if [ "$EUID" -ne 0 ]; then
    echo "Run with sudo"
    exit 1
fi

# Clean state
rm -rf /var/lib/appmon
rm -f /usr/local/bin/appmon

# Build
make build

# Install
./build/appmon start

# Verify files
test -f /var/lib/appmon/.key
test -f /var/lib/appmon/registry.db

# Verify daemons running
./build/appmon status | grep -q "RUNNING"

# Verify plist in system location
ls /Library/LaunchDaemons/ | grep -q "com.apple"

echo "PASS: Fresh install system mode"
```

---

### E2E-3: Migration from Old Version

**Script**: `test/e2e/migration.sh`

```bash
#!/bin/bash
set -e

# Setup: install old version (without encrypted registry)
git checkout v0.3.x  # or whatever version
make build
./build/appmon start
./build/appmon status | grep -q "RUNNING"

# Verify old registry exists
test -f ~/.appmon/registry.json  # or wherever old path is

# Upgrade to new version
git checkout main
make build
./build/appmon start

# Verify migration occurred
test -f ~/.appmon/registry.db
test ! -f ~/.appmon/registry.json  # old file deleted

# Verify still running
./build/appmon status | grep -q "RUNNING"

echo "PASS: Migration"
```

---

### E2E-4: Tamper Resistance

**Script**: `test/e2e/tamper_resistance.sh`

```bash
#!/bin/bash
set -e

# Install
./build/appmon start

# Try to read registry
echo "Attempting to read encrypted registry..."
if sqlite3 ~/.appmon/registry.db "SELECT * FROM daemons" 2>/dev/null; then
    echo "FAIL: Registry readable without key"
    exit 1
fi

echo "PASS: Registry not readable without key"

# Try to find plist by known name
echo "Checking plist name..."
if ls ~/Library/LaunchAgents/com.focusd.appmon.plist 2>/dev/null; then
    echo "FAIL: Plist has predictable name"
    exit 1
fi

echo "PASS: Plist has random name"

# Verify plist exists with random name
PLIST_COUNT=$(ls ~/Library/LaunchAgents/com.apple.*.plist 2>/dev/null | wc -l)
if [ "$PLIST_COUNT" -eq 0 ]; then
    echo "FAIL: No plist found"
    exit 1
fi

echo "PASS: Tamper resistance verified"
```

---

### E2E-5: Version Upgrade with Encrypted Registry

**Script**: `test/e2e/upgrade_encrypted.sh`

```bash
#!/bin/bash
set -e

# Install v1 with encrypted registry
VERSION=0.4.0 make build
cp build/appmon build/appmon-v1
./build/appmon-v1 start

# Get current secrets
PLIST_V1=$(ls ~/Library/LaunchAgents/com.apple.*.plist)

# Build v2
VERSION=0.4.1 make build
cp build/appmon build/appmon-v2

# Upgrade
./build/appmon-v2 start

# Verify same plist (secrets preserved)
PLIST_V2=$(ls ~/Library/LaunchAgents/com.apple.*.plist)
if [ "$PLIST_V1" != "$PLIST_V2" ]; then
    echo "FAIL: Plist changed after upgrade"
    exit 1
fi

echo "PASS: Secrets preserved after upgrade"
```

---

## Test Matrix

| Test | User Mode | System Mode | Migration | Notes |
|------|-----------|-------------|-----------|-------|
| UT-1.* | Auto | Auto | N/A | Unit test |
| UT-2.* | Auto | Auto | N/A | Unit test |
| UT-3.* | Auto | Auto | N/A | Unit test |
| UT-4.* | Auto | Auto | N/A | Unit test |
| UT-5.* | Auto | Auto | Yes | Unit test |
| IT-1.* | Auto | Auto | N/A | Integration |
| IT-2.* | Manual | Manual | N/A | Needs launchctl |
| IT-3.* | Auto | Auto | N/A | Integration |
| E2E-1 | Manual | N/A | N/A | Shell script |
| E2E-2 | N/A | Manual | N/A | Needs sudo |
| E2E-3 | Manual | Manual | Yes | Needs old version |
| E2E-4 | Manual | N/A | N/A | Shell script |
| E2E-5 | Manual | N/A | N/A | Shell script |

---

## CI/CD Considerations

1. **CGO Required**: CI must have CGO_ENABLED=1
2. **OpenSSL**: May need `apt-get install libssl-dev` or `brew install openssl`
3. **E2E tests**: Run in separate job with clean VM
4. **System mode tests**: Need privileged container or skip in CI

---

## Acceptance Criteria

- [ ] All unit tests pass
- [ ] All integration tests pass
- [ ] E2E-1 passes (fresh install user mode)
- [ ] E2E-2 passes (fresh install system mode)
- [ ] E2E-3 passes (migration)
- [ ] E2E-4 passes (tamper resistance)
- [ ] E2E-5 passes (upgrade preserves secrets)
- [ ] CI builds successfully with CGO
