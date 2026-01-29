package infra

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
	sqlcipher "github.com/mutecomm/go-sqlcipher/v4"
)

// Ensure sqlcipher driver is registered.
var _ = sqlcipher.ErrBusy

const (
	registryDBName = "registry.db"
)

// EncryptedRegistry implements domain.DaemonRegistry and domain.SecretStore
// using a SQLCipher encrypted SQLite database.
type EncryptedRegistry struct {
	db             *sql.DB
	dbPath         string
	processManager domain.ProcessManager
}

// NewEncryptedRegistry opens (or creates) an encrypted registry database.
// The key is used as the SQLCipher passphrase via PRAGMA key.
func NewEncryptedRegistry(dataDir string, key []byte, pm domain.ProcessManager) (*EncryptedRegistry, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	dbPath := filepath.Join(dataDir, registryDBName)
	keyHex := hex.EncodeToString(key)

	// Open with SQLCipher key as DSN parameter
	dsn := fmt.Sprintf("%s?_pragma_key=x'%s'&_pragma_cipher_page_size=4096", dbPath, keyHex)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open encrypted database: %w", err)
	}

	// Verify encryption works by running a query
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to connect to encrypted database: %w", err)
	}

	reg := &EncryptedRegistry{
		db:             db,
		dbPath:         dbPath,
		processManager: pm,
	}

	if err := reg.createTables(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	return reg, nil
}

// createTables creates the schema if it doesn't exist.
func (r *EncryptedRegistry) createTables() error {
	schema := `
	CREATE TABLE IF NOT EXISTS daemon_state (
		role TEXT PRIMARY KEY,
		pid INTEGER NOT NULL,
		process_name TEXT NOT NULL,
		last_heartbeat INTEGER NOT NULL,
		app_version TEXT DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS secrets (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);

	CREATE TABLE IF NOT EXISTS meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	`
	_, err := r.db.Exec(schema)
	return err
}

// --- domain.DaemonRegistry implementation ---

// Register saves current daemon's PID and obfuscated name.
func (r *EncryptedRegistry) Register(daemon domain.Daemon) error {
	now := time.Now().Unix()

	// Auto-detect mode
	mode := "user"
	if os.Geteuid() == 0 {
		mode = "system"
	}

	_, err := r.db.Exec(`
		INSERT OR REPLACE INTO daemon_state (role, pid, process_name, last_heartbeat, app_version)
		VALUES (?, ?, ?, ?, ?)`,
		string(daemon.Role), daemon.PID, daemon.ObfuscatedName, now, daemon.AppVersion,
	)
	if err != nil {
		return err
	}

	// Store mode and version in meta
	_, err = r.db.Exec(`INSERT OR REPLACE INTO meta (key, value) VALUES ('mode', ?)`, mode)
	if err != nil {
		return err
	}
	if daemon.AppVersion != "" {
		_, err = r.db.Exec(`INSERT OR REPLACE INTO meta (key, value) VALUES ('app_version', ?)`, daemon.AppVersion)
	}
	return err
}

// GetPartner returns the partner daemon info (watcher<->guardian).
func (r *EncryptedRegistry) GetPartner(role domain.DaemonRole) (*domain.Daemon, error) {
	var partnerRole domain.DaemonRole
	switch role {
	case domain.RoleWatcher:
		partnerRole = domain.RoleGuardian
	case domain.RoleGuardian:
		partnerRole = domain.RoleWatcher
	}

	var pid int
	var name string
	err := r.db.QueryRow(`SELECT pid, process_name FROM daemon_state WHERE role = ?`,
		string(partnerRole)).Scan(&pid, &name)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("partner %s not registered", partnerRole)
	}
	if err != nil {
		return nil, err
	}

	if pid == 0 {
		return nil, fmt.Errorf("partner %s not registered", partnerRole)
	}

	return &domain.Daemon{
		PID:            pid,
		Role:           partnerRole,
		ObfuscatedName: name,
	}, nil
}

// UpdateHeartbeat updates timestamp for liveness check.
func (r *EncryptedRegistry) UpdateHeartbeat(role domain.DaemonRole) error {
	now := time.Now().Unix()
	result, err := r.db.Exec(`UPDATE daemon_state SET last_heartbeat = ? WHERE role = ?`,
		now, string(role))
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("daemon %s not registered", role)
	}
	return nil
}

// IsPartnerAlive checks if partner daemon is running via PID.
func (r *EncryptedRegistry) IsPartnerAlive(role domain.DaemonRole) (bool, error) {
	partner, err := r.GetPartner(role)
	if err != nil {
		return false, nil // Partner not registered = not alive
	}
	return r.processManager.IsRunning(partner.PID), nil
}

// GetAll returns full registry state (for status command).
// Maps SQLCipher data back to RegistryEntry for backward compatibility.
func (r *EncryptedRegistry) GetAll() (*domain.RegistryEntry, error) {
	entry := &domain.RegistryEntry{Version: 1}

	// Read daemon states
	rows, err := r.db.Query(`SELECT role, pid, process_name, last_heartbeat, app_version FROM daemon_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var role string
		var pid int
		var name string
		var heartbeat int64
		var appVersion string
		if err := rows.Scan(&role, &pid, &name, &heartbeat, &appVersion); err != nil {
			return nil, err
		}
		found = true
		switch domain.DaemonRole(role) {
		case domain.RoleWatcher:
			entry.WatcherPID = pid
			entry.WatcherName = name
			entry.AppVersion = appVersion
		case domain.RoleGuardian:
			entry.GuardianPID = pid
			entry.GuardianName = name
		}
		if heartbeat > entry.LastHeartbeat {
			entry.LastHeartbeat = heartbeat
		}
	}

	if !found {
		return nil, nil
	}

	// Read mode from meta
	var mode string
	err = r.db.QueryRow(`SELECT value FROM meta WHERE key = 'mode'`).Scan(&mode)
	if err == nil {
		entry.Mode = mode
	}

	return entry, nil
}

// Clear removes all daemon state (for clean restart).
func (r *EncryptedRegistry) Clear() error {
	_, err := r.db.Exec(`DELETE FROM daemon_state`)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`DELETE FROM meta WHERE key IN ('mode', 'app_version')`)
	return err
}

// GetRegistryPath returns the database file path.
func (r *EncryptedRegistry) GetRegistryPath() string {
	return r.dbPath
}

// --- domain.SecretStore implementation ---

// GetSecret retrieves a secret by key.
func (r *EncryptedRegistry) GetSecret(key string) (string, error) {
	var value string
	err := r.db.QueryRow(`SELECT value FROM secrets WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("secret %q not found", key)
	}
	return value, err
}

// SetSecret stores a secret.
func (r *EncryptedRegistry) SetSecret(key, value string) error {
	now := time.Now().Unix()
	_, err := r.db.Exec(`INSERT OR REPLACE INTO secrets (key, value, created_at) VALUES (?, ?, ?)`,
		key, value, now)
	return err
}

// GetAllSecrets returns all stored secrets.
func (r *EncryptedRegistry) GetAllSecrets() (map[string]string, error) {
	rows, err := r.db.Query(`SELECT key, value FROM secrets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	secrets := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		secrets[k] = v
	}
	return secrets, nil
}

// Close releases the database connection.
func (r *EncryptedRegistry) Close() error {
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}

// Ensure EncryptedRegistry implements both interfaces.
var _ domain.DaemonRegistry = (*EncryptedRegistry)(nil)
var _ domain.SecretStore = (*EncryptedRegistry)(nil)
