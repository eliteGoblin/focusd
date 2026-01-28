package infra

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

const registryDir = "/var/tmp"

// FileRegistry implements domain.DaemonRegistry using a hidden JSON file.
// The file location is obfuscated using a hash of the hostname.
type FileRegistry struct {
	path           string
	processManager domain.ProcessManager
}

// NewFileRegistry creates a new file-based daemon registry.
func NewFileRegistry(pm domain.ProcessManager) domain.DaemonRegistry {
	// Generate obfuscated filename based on machine identifier
	hostname, _ := os.Hostname()
	hash := md5.Sum([]byte("appmon-registry-" + hostname))
	filename := ".cf_sys_registry_" + hex.EncodeToString(hash[:])[:8]

	return &FileRegistry{
		path:           filepath.Join(registryDir, filename),
		processManager: pm,
	}
}

// NewFileRegistryWithPath creates a registry at a specific path (for testing).
func NewFileRegistryWithPath(path string, pm domain.ProcessManager) domain.DaemonRegistry {
	return &FileRegistry{
		path:           path,
		processManager: pm,
	}
}

// GetRegistryPath returns the hidden registry file path.
func (r *FileRegistry) GetRegistryPath() string {
	return r.path
}

// Register saves current daemon's PID and obfuscated name.
func (r *FileRegistry) Register(daemon domain.Daemon) error {
	// Use file lock to prevent race condition between watcher and guardian
	lockPath := r.path + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("failed to open lock file: %w", err)
	}
	defer lockFile.Close()

	// Acquire exclusive lock
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()

	entry, _ := r.GetAll() // May not exist yet
	if entry == nil {
		entry = &domain.RegistryEntry{Version: 1}
	}

	switch daemon.Role {
	case domain.RoleWatcher:
		entry.WatcherPID = daemon.PID
		entry.WatcherName = daemon.ObfuscatedName
	case domain.RoleGuardian:
		entry.GuardianPID = daemon.PID
		entry.GuardianName = daemon.ObfuscatedName
	}
	entry.LastHeartbeat = time.Now().Unix()

	// Store app version from daemon
	if daemon.AppVersion != "" {
		entry.AppVersion = daemon.AppVersion
	}

	// Auto-detect and store execution mode
	if os.Geteuid() == 0 {
		entry.Mode = "system"
	} else {
		entry.Mode = "user"
	}

	return r.atomicWrite(entry)
}

// GetPartner returns the partner daemon info.
func (r *FileRegistry) GetPartner(role domain.DaemonRole) (*domain.Daemon, error) {
	entry, err := r.GetAll()
	if err != nil {
		return nil, err
	}

	var partnerRole domain.DaemonRole
	var pid int
	var name string

	switch role {
	case domain.RoleWatcher:
		// Watcher's partner is guardian
		partnerRole = domain.RoleGuardian
		pid = entry.GuardianPID
		name = entry.GuardianName
	case domain.RoleGuardian:
		// Guardian's partner is watcher
		partnerRole = domain.RoleWatcher
		pid = entry.WatcherPID
		name = entry.WatcherName
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
func (r *FileRegistry) UpdateHeartbeat(role domain.DaemonRole) error {
	entry, err := r.GetAll()
	if err != nil {
		return err
	}

	entry.LastHeartbeat = time.Now().Unix()
	return r.atomicWrite(entry)
}

// IsPartnerAlive checks if partner daemon is running via PID.
func (r *FileRegistry) IsPartnerAlive(role domain.DaemonRole) (bool, error) {
	partner, err := r.GetPartner(role)
	if err != nil {
		return false, nil // Partner not registered = not alive
	}

	return r.processManager.IsRunning(partner.PID), nil
}

// GetAll returns full registry state.
func (r *FileRegistry) GetAll() (*domain.RegistryEntry, error) {
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var entry domain.RegistryEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, err
	}

	return &entry, nil
}

// Clear removes registry file.
func (r *FileRegistry) Clear() error {
	return os.Remove(r.path)
}

// atomicWrite writes registry to file atomically (write + rename).
func (r *FileRegistry) atomicWrite(entry *domain.RegistryEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	// Write to temp file first (unique per process to avoid race)
	tmpPath := fmt.Sprintf("%s.%d.tmp", r.path, os.Getpid())
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}

	// Atomic rename
	if err := os.Rename(tmpPath, r.path); err != nil {
		os.Remove(tmpPath) // Clean up on failure
		return err
	}
	return nil
}

// Ensure FileRegistry implements domain.DaemonRegistry.
var _ domain.DaemonRegistry = (*FileRegistry)(nil)
