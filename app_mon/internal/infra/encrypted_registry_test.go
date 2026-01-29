package infra

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// newTestRegistry creates an encrypted registry in a temp directory for testing.
func newTestRegistry(t *testing.T) (*EncryptedRegistry, string) {
	t.Helper()
	dataDir := t.TempDir()
	key, err := GenerateKey()
	require.NoError(t, err)

	pm := newMockProcessManager()
	reg, err := NewEncryptedRegistry(dataDir, key, pm)
	require.NoError(t, err)

	t.Cleanup(func() { reg.Close() })
	return reg, dataDir
}

// newTestRegistryWithPM creates an encrypted registry with a custom process manager.
func newTestRegistryWithPM(t *testing.T, pm *mockProcessManager) *EncryptedRegistry {
	t.Helper()
	dataDir := t.TempDir()
	key, err := GenerateKey()
	require.NoError(t, err)

	reg, err := NewEncryptedRegistry(dataDir, key, pm)
	require.NoError(t, err)

	t.Cleanup(func() { reg.Close() })
	return reg
}

func TestEncryptedRegistry_Register(t *testing.T) {
	tests := []struct {
		name     string
		daemons  []domain.Daemon
		wantPIDs map[domain.DaemonRole]int
	}{
		{
			name: "register watcher",
			daemons: []domain.Daemon{
				{PID: 1234, Role: domain.RoleWatcher, ObfuscatedName: "com.apple.test.abc123", AppVersion: "0.5.0"},
			},
			wantPIDs: map[domain.DaemonRole]int{domain.RoleWatcher: 1234},
		},
		{
			name: "register both daemons",
			daemons: []domain.Daemon{
				{PID: 1234, Role: domain.RoleWatcher, ObfuscatedName: "watcher-name", AppVersion: "0.5.0"},
				{PID: 5678, Role: domain.RoleGuardian, ObfuscatedName: "guardian-name", AppVersion: "0.5.0"},
			},
			wantPIDs: map[domain.DaemonRole]int{
				domain.RoleWatcher:  1234,
				domain.RoleGuardian: 5678,
			},
		},
		{
			name: "re-register overwrites PID",
			daemons: []domain.Daemon{
				{PID: 1111, Role: domain.RoleWatcher, ObfuscatedName: "old-name"},
				{PID: 2222, Role: domain.RoleWatcher, ObfuscatedName: "new-name"},
			},
			wantPIDs: map[domain.DaemonRole]int{domain.RoleWatcher: 2222},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, _ := newTestRegistry(t)

			for _, d := range tt.daemons {
				require.NoError(t, reg.Register(d))
			}

			entry, err := reg.GetAll()
			require.NoError(t, err)
			require.NotNil(t, entry)

			for role, wantPID := range tt.wantPIDs {
				switch role {
				case domain.RoleWatcher:
					assert.Equal(t, wantPID, entry.WatcherPID)
				case domain.RoleGuardian:
					assert.Equal(t, wantPID, entry.GuardianPID)
				}
			}
		})
	}
}

func TestEncryptedRegistry_GetPartner(t *testing.T) {
	tests := []struct {
		name      string
		register  []domain.Daemon
		queryRole domain.DaemonRole
		wantPID   int
		wantName  string
		wantErr   bool
	}{
		{
			name: "watcher gets guardian as partner",
			register: []domain.Daemon{
				{PID: 1234, Role: domain.RoleWatcher, ObfuscatedName: "watcher"},
				{PID: 5678, Role: domain.RoleGuardian, ObfuscatedName: "guardian"},
			},
			queryRole: domain.RoleWatcher,
			wantPID:   5678,
			wantName:  "guardian",
		},
		{
			name: "guardian gets watcher as partner",
			register: []domain.Daemon{
				{PID: 1234, Role: domain.RoleWatcher, ObfuscatedName: "watcher"},
				{PID: 5678, Role: domain.RoleGuardian, ObfuscatedName: "guardian"},
			},
			queryRole: domain.RoleGuardian,
			wantPID:   1234,
			wantName:  "watcher",
		},
		{
			name: "error when partner not registered",
			register: []domain.Daemon{
				{PID: 1234, Role: domain.RoleWatcher, ObfuscatedName: "watcher"},
			},
			queryRole: domain.RoleWatcher,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, _ := newTestRegistry(t)

			for _, d := range tt.register {
				require.NoError(t, reg.Register(d))
			}

			partner, err := reg.GetPartner(tt.queryRole)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantPID, partner.PID)
			assert.Equal(t, tt.wantName, partner.ObfuscatedName)
		})
	}
}

func TestEncryptedRegistry_IsPartnerAlive(t *testing.T) {
	tests := []struct {
		name      string
		register  []domain.Daemon
		running   map[int]bool
		queryRole domain.DaemonRole
		wantAlive bool
	}{
		{
			name: "partner alive when PID running",
			register: []domain.Daemon{
				{PID: 1234, Role: domain.RoleWatcher, ObfuscatedName: "w"},
				{PID: 5678, Role: domain.RoleGuardian, ObfuscatedName: "g"},
			},
			running:   map[int]bool{5678: true},
			queryRole: domain.RoleWatcher,
			wantAlive: true,
		},
		{
			name: "partner dead when PID not running",
			register: []domain.Daemon{
				{PID: 1234, Role: domain.RoleWatcher, ObfuscatedName: "w"},
				{PID: 5678, Role: domain.RoleGuardian, ObfuscatedName: "g"},
			},
			running:   map[int]bool{5678: false},
			queryRole: domain.RoleWatcher,
			wantAlive: false,
		},
		{
			name:      "not alive when partner not registered",
			register:  []domain.Daemon{{PID: 1234, Role: domain.RoleWatcher, ObfuscatedName: "w"}},
			running:   map[int]bool{},
			queryRole: domain.RoleWatcher,
			wantAlive: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := newMockProcessManager()
			for pid, running := range tt.running {
				pm.SetRunning(pid, running)
			}

			reg := newTestRegistryWithPM(t, pm)

			for _, d := range tt.register {
				require.NoError(t, reg.Register(d))
			}

			alive, err := reg.IsPartnerAlive(tt.queryRole)
			require.NoError(t, err)
			assert.Equal(t, tt.wantAlive, alive)
		})
	}
}

func TestEncryptedRegistry_UpdateHeartbeat(t *testing.T) {
	tests := []struct {
		name    string
		role    domain.DaemonRole
		wantErr bool
	}{
		{
			name: "update heartbeat for registered daemon",
			role: domain.RoleWatcher,
		},
		{
			name:    "error for unregistered daemon",
			role:    domain.RoleGuardian,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, _ := newTestRegistry(t)

			// Only register watcher
			require.NoError(t, reg.Register(domain.Daemon{
				PID: 1234, Role: domain.RoleWatcher, ObfuscatedName: "w",
			}))

			err := reg.UpdateHeartbeat(tt.role)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEncryptedRegistry_GetAll(t *testing.T) {
	tests := []struct {
		name     string
		register []domain.Daemon
		wantNil  bool
	}{
		{
			name:    "returns nil when empty",
			wantNil: true,
		},
		{
			name: "returns entry with both daemons",
			register: []domain.Daemon{
				{PID: 1234, Role: domain.RoleWatcher, ObfuscatedName: "w", AppVersion: "0.5.0"},
				{PID: 5678, Role: domain.RoleGuardian, ObfuscatedName: "g", AppVersion: "0.5.0"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, _ := newTestRegistry(t)

			for _, d := range tt.register {
				require.NoError(t, reg.Register(d))
			}

			entry, err := reg.GetAll()
			require.NoError(t, err)

			if tt.wantNil {
				assert.Nil(t, entry)
				return
			}

			require.NotNil(t, entry)
			assert.Equal(t, 1234, entry.WatcherPID)
			assert.Equal(t, 5678, entry.GuardianPID)
			assert.Equal(t, "w", entry.WatcherName)
			assert.Equal(t, "g", entry.GuardianName)
			assert.Equal(t, "0.5.0", entry.AppVersion)
			assert.True(t, entry.LastHeartbeat > 0)
		})
	}
}

func TestEncryptedRegistry_Clear(t *testing.T) {
	reg, _ := newTestRegistry(t)

	// Register daemons
	require.NoError(t, reg.Register(domain.Daemon{
		PID: 1234, Role: domain.RoleWatcher, ObfuscatedName: "w",
	}))

	// Clear
	require.NoError(t, reg.Clear())

	// GetAll should return nil after clear
	entry, err := reg.GetAll()
	require.NoError(t, err)
	assert.Nil(t, entry)
}

func TestEncryptedRegistry_Secrets(t *testing.T) {
	tests := []struct {
		name   string
		testFn func(t *testing.T, reg *EncryptedRegistry)
	}{
		{
			name: "set and get secret",
			testFn: func(t *testing.T, reg *EncryptedRegistry) {
				require.NoError(t, reg.SetSecret("plist_name", "com.apple.xpc.helper.abc123"))
				val, err := reg.GetSecret("plist_name")
				require.NoError(t, err)
				assert.Equal(t, "com.apple.xpc.helper.abc123", val)
			},
		},
		{
			name: "get nonexistent secret returns error",
			testFn: func(t *testing.T, reg *EncryptedRegistry) {
				_, err := reg.GetSecret("nonexistent")
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "not found")
			},
		},
		{
			name: "overwrite secret",
			testFn: func(t *testing.T, reg *EncryptedRegistry) {
				require.NoError(t, reg.SetSecret("key", "value1"))
				require.NoError(t, reg.SetSecret("key", "value2"))

				val, err := reg.GetSecret("key")
				require.NoError(t, err)
				assert.Equal(t, "value2", val)
			},
		},
		{
			name: "get all secrets",
			testFn: func(t *testing.T, reg *EncryptedRegistry) {
				require.NoError(t, reg.SetSecret("a", "1"))
				require.NoError(t, reg.SetSecret("b", "2"))
				require.NoError(t, reg.SetSecret("c", "3"))

				secrets, err := reg.GetAllSecrets()
				require.NoError(t, err)
				assert.Len(t, secrets, 3)
				assert.Equal(t, "1", secrets["a"])
				assert.Equal(t, "2", secrets["b"])
				assert.Equal(t, "3", secrets["c"])
			},
		},
		{
			name: "clear does not remove secrets",
			testFn: func(t *testing.T, reg *EncryptedRegistry) {
				require.NoError(t, reg.SetSecret("plist_name", "test"))
				require.NoError(t, reg.Register(domain.Daemon{
					PID: 1234, Role: domain.RoleWatcher, ObfuscatedName: "w",
				}))

				// Clear removes daemon state but NOT secrets
				require.NoError(t, reg.Clear())

				val, err := reg.GetSecret("plist_name")
				require.NoError(t, err)
				assert.Equal(t, "test", val)

				entry, err := reg.GetAll()
				require.NoError(t, err)
				assert.Nil(t, entry)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, _ := newTestRegistry(t)
			tt.testFn(t, reg)
		})
	}
}

func TestEncryptedRegistry_Encryption(t *testing.T) {
	tests := []struct {
		name   string
		testFn func(t *testing.T)
	}{
		{
			name: "database file is unreadable without key",
			testFn: func(t *testing.T) {
				dataDir := t.TempDir()
				key, err := GenerateKey()
				require.NoError(t, err)

				pm := newMockProcessManager()
				reg, err := NewEncryptedRegistry(dataDir, key, pm)
				require.NoError(t, err)

				// Write some data
				require.NoError(t, reg.SetSecret("test", "secret_value"))
				reg.Close()

				// Try to read the raw file - should not contain plaintext
				dbPath := filepath.Join(dataDir, registryDBName)
				rawData, err := os.ReadFile(dbPath)
				require.NoError(t, err)
				assert.NotContains(t, string(rawData), "secret_value")
				assert.NotContains(t, string(rawData), "test")
			},
		},
		{
			name: "wrong key fails to open",
			testFn: func(t *testing.T) {
				dataDir := t.TempDir()
				key1, _ := GenerateKey()
				key2, _ := GenerateKey()

				pm := newMockProcessManager()

				// Create DB with key1
				reg1, err := NewEncryptedRegistry(dataDir, key1, pm)
				require.NoError(t, err)
				require.NoError(t, reg1.SetSecret("test", "value"))
				reg1.Close()

				// Try to open with key2 - should fail on table creation (encrypted DB unreadable)
				_, err = NewEncryptedRegistry(dataDir, key2, pm)
				assert.Error(t, err)
			},
		},
		{
			name: "correct key reads data",
			testFn: func(t *testing.T) {
				dataDir := t.TempDir()
				key, _ := GenerateKey()
				pm := newMockProcessManager()

				// Write with key
				reg1, err := NewEncryptedRegistry(dataDir, key, pm)
				require.NoError(t, err)
				require.NoError(t, reg1.SetSecret("test", "secret_value"))
				reg1.Close()

				// Read with same key
				reg2, err := NewEncryptedRegistry(dataDir, key, pm)
				require.NoError(t, err)
				defer reg2.Close()

				val, err := reg2.GetSecret("test")
				require.NoError(t, err)
				assert.Equal(t, "secret_value", val)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.testFn)
	}
}

func TestEncryptedRegistry_GetAll_IncludesMode(t *testing.T) {
	reg, _ := newTestRegistry(t)

	// Register a daemon (this also stores mode in meta)
	require.NoError(t, reg.Register(domain.Daemon{
		PID: 1234, Role: domain.RoleWatcher, ObfuscatedName: "w", AppVersion: "0.5.0",
	}))

	entry, err := reg.GetAll()
	require.NoError(t, err)
	require.NotNil(t, entry)
	// Mode is set based on euid; in tests it's "user" (not root)
	assert.Equal(t, "user", entry.Mode)
}

func TestEncryptedRegistry_Register_StoresAppVersion(t *testing.T) {
	reg, _ := newTestRegistry(t)

	require.NoError(t, reg.Register(domain.Daemon{
		PID: 1234, Role: domain.RoleWatcher, ObfuscatedName: "w", AppVersion: "0.5.0",
	}))

	// Verify app_version is stored in meta table
	var version string
	err := reg.db.QueryRow(`SELECT value FROM meta WHERE key = 'app_version'`).Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, "0.5.0", version)
}

func TestEncryptedRegistry_Register_EmptyAppVersion(t *testing.T) {
	reg, _ := newTestRegistry(t)

	// Register without app version - should not store empty in meta
	require.NoError(t, reg.Register(domain.Daemon{
		PID: 1234, Role: domain.RoleWatcher, ObfuscatedName: "w", AppVersion: "",
	}))

	// app_version meta should not exist
	var version string
	err := reg.db.QueryRow(`SELECT value FROM meta WHERE key = 'app_version'`).Scan(&version)
	assert.Error(t, err) // sql.ErrNoRows
}

func TestEncryptedRegistry_Close_Idempotent(t *testing.T) {
	dataDir := t.TempDir()
	key, err := GenerateKey()
	require.NoError(t, err)

	pm := newMockProcessManager()
	reg, err := NewEncryptedRegistry(dataDir, key, pm)
	require.NoError(t, err)

	// First close should succeed
	assert.NoError(t, reg.Close())

	// Second close - db is closed but pointer is still non-nil
	// This tests the nil check path
	reg.db = nil
	assert.NoError(t, reg.Close())
}

func TestEncryptedRegistry_GetAllSecrets_Empty(t *testing.T) {
	reg, _ := newTestRegistry(t)

	secrets, err := reg.GetAllSecrets()
	require.NoError(t, err)
	assert.Empty(t, secrets)
}

func TestEncryptedRegistry_GetRegistryPath(t *testing.T) {
	reg, dataDir := newTestRegistry(t)
	expected := filepath.Join(dataDir, registryDBName)
	assert.Equal(t, expected, reg.GetRegistryPath())
}
