package infra

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

const (
	// DefaultHealthCheckTimeout is how long to wait for daemons after update
	DefaultHealthCheckTimeout = 10 * time.Second
	// DaemonCheckInterval is how often to check daemon status
	DaemonCheckInterval = 500 * time.Millisecond
)

// UpdateResult contains the result of an update operation
type UpdateResult struct {
	Success        bool
	PreviousVer    string
	NewVer         string
	RolledBack     bool
	RollbackReason string
}

// Updater handles self-update operations with rollback support
type Updater struct {
	downloader     *GitHubDownloader
	backupManager  *BackupManager
	registry       domain.DaemonRegistry
	pm             domain.ProcessManager
	execMode       *ExecModeConfig
	currentVersion string
	logger         *zap.Logger
}

// NewUpdater creates a new Updater instance
func NewUpdater(currentVersion string, logger *zap.Logger) *Updater {
	execMode := DetectExecMode()
	pm := NewProcessManager()

	return &Updater{
		downloader:     NewGitHubDownloader(),
		backupManager:  NewBackupManager(),
		registry:       NewFileRegistry(pm),
		pm:             pm,
		execMode:       execMode,
		currentVersion: currentVersion,
		logger:         logger,
	}
}

// NewUpdaterWithDeps creates an Updater with injected dependencies (for testing)
func NewUpdaterWithDeps(
	downloader *GitHubDownloader,
	backupManager *BackupManager,
	registry domain.DaemonRegistry,
	pm domain.ProcessManager,
	execMode *ExecModeConfig,
	currentVersion string,
	logger *zap.Logger,
) *Updater {
	return &Updater{
		downloader:     downloader,
		backupManager:  backupManager,
		registry:       registry,
		pm:             pm,
		execMode:       execMode,
		currentVersion: currentVersion,
		logger:         logger,
	}
}

// CheckUpdate checks if an update is available
// Returns: current version, latest version, update available, error
func (u *Updater) CheckUpdate() (current string, latest string, available bool, err error) {
	current = u.currentVersion

	latest, err = u.downloader.GetLatestVersion()
	if err != nil {
		return current, "", false, fmt.Errorf("failed to get latest version: %w", err)
	}

	// Compare versions
	available = isNewerVersion(latest, current)
	return current, latest, available, nil
}

// PerformUpdate downloads and installs the latest version with rollback support
func (u *Updater) PerformUpdate() (*UpdateResult, error) {
	result := &UpdateResult{
		PreviousVer: u.currentVersion,
	}

	// Step 1: Check for update
	_, latest, available, err := u.CheckUpdate()
	if err != nil {
		return nil, fmt.Errorf("failed to check for update: %w", err)
	}

	if !available {
		result.Success = true
		result.NewVer = u.currentVersion
		return result, nil // Already up to date
	}
	result.NewVer = latest

	u.log("update available", zap.String("current", u.currentVersion), zap.String("latest", latest))

	// Step 2: Get current binary path
	binaryPath := u.execMode.BinaryPath
	config, err := u.backupManager.GetConfig()
	if err == nil && config.MainBinaryPath != "" {
		binaryPath = config.MainBinaryPath
	}

	// Step 3: Create rollback backup (separate from regular backups)
	rollbackPath, err := u.createRollbackBackup(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create rollback backup: %w", err)
	}
	defer os.RemoveAll(filepath.Dir(rollbackPath)) // Clean up rollback dir after success

	u.log("rollback backup created", zap.String("path", rollbackPath))

	// Step 4: Download new binary to temp location
	u.log("downloading new version", zap.String("version", latest))
	tmpBinaryPath, err := u.downloader.DownloadToTemp()
	if err != nil {
		return nil, fmt.Errorf("failed to download update: %w", err)
	}
	defer os.RemoveAll(filepath.Dir(tmpBinaryPath)) // Clean up temp dir

	u.log("download complete", zap.String("path", tmpBinaryPath))

	// Use shared installation logic
	return u.performInstall(result, tmpBinaryPath, binaryPath, rollbackPath, latest)
}

// PerformUpdateFromLocal installs a local binary with rollback support (for testing)
func (u *Updater) PerformUpdateFromLocal(localBinaryPath string) (*UpdateResult, error) {
	result := &UpdateResult{
		PreviousVer: u.currentVersion,
	}

	// Validate local binary exists and is executable
	info, err := os.Stat(localBinaryPath)
	if err != nil {
		return nil, fmt.Errorf("local binary not found: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("local binary path is a directory")
	}

	// Get version from the local binary
	newVersion, err := u.getVersionFromBinary(localBinaryPath)
	if err != nil {
		u.log("warning: could not get version from local binary", zap.Error(err))
		newVersion = "unknown"
	}
	result.NewVer = newVersion

	u.log("updating from local binary", zap.String("path", localBinaryPath), zap.String("version", newVersion))

	// Get current binary path
	binaryPath := u.execMode.BinaryPath
	config, err := u.backupManager.GetConfig()
	if err == nil && config.MainBinaryPath != "" {
		binaryPath = config.MainBinaryPath
	}

	// Create rollback backup
	rollbackPath, err := u.createRollbackBackup(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create rollback backup: %w", err)
	}
	defer os.RemoveAll(filepath.Dir(rollbackPath))

	u.log("rollback backup created", zap.String("path", rollbackPath))

	// Use shared installation logic
	return u.performInstall(result, localBinaryPath, binaryPath, rollbackPath, newVersion)
}

// getVersionFromBinary runs the binary with "version" to extract version string
func (u *Updater) getVersionFromBinary(binaryPath string) (string, error) {
	cmd := exec.Command(binaryPath, "version")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	// Parse "appmon X.Y.Z (...)" format
	parts := strings.Fields(string(output))
	if len(parts) >= 2 {
		return parts[1], nil
	}
	return strings.TrimSpace(string(output)), nil
}

// performInstall handles the common installation logic for both GitHub and local updates
func (u *Updater) performInstall(result *UpdateResult, srcBinaryPath, dstBinaryPath, rollbackPath, newVersion string) (*UpdateResult, error) {
	// Stop daemons
	u.log("stopping daemons")
	if err := u.StopDaemons(); err != nil {
		// Check if it's a registry error (critical) vs signaling error (non-critical)
		if isRegistryError(err) {
			return nil, fmt.Errorf("failed to access daemon registry: %w", err)
		}
		u.log("warning: failed to stop some daemons", zap.Error(err))
		// Continue - signaling errors are non-critical, daemons may not be running
	}

	// Replace main binary (atomic)
	u.log("installing new binary", zap.String("path", dstBinaryPath))
	if err := copyFile(srcBinaryPath, dstBinaryPath); err != nil {
		// Restore from rollback and restart daemons
		u.log("install failed, rolling back", zap.Error(err))
		if rbErr := copyFile(rollbackPath, dstBinaryPath); rbErr != nil {
			return nil, fmt.Errorf("critical: install failed and rollback failed: install=%w, rollback=%v", err, rbErr)
		}
		_ = os.Chmod(dstBinaryPath, 0755)
		// Restart daemons with original binary
		if startErr := u.StartDaemons(); startErr != nil {
			u.log("warning: failed to restart daemons after install rollback", zap.Error(startErr))
		}
		result.RolledBack = true
		result.RollbackReason = fmt.Sprintf("install failed: %v", err)
		return result, nil
	}
	_ = os.Chmod(dstBinaryPath, 0755)

	// Update all backup copies
	u.log("updating backup copies")
	if err := u.backupManager.SetupBackupsWithMode(dstBinaryPath, newVersion, "", u.execMode.Mode); err != nil {
		u.log("backup update failed, rolling back", zap.Error(err))
		result.RolledBack = true
		result.RollbackReason = fmt.Sprintf("failed to update backups: %v", err)
		if rbErr := u.rollback(rollbackPath, dstBinaryPath); rbErr != nil {
			return nil, fmt.Errorf("critical: backup update failed and rollback failed: backup=%w, rollback=%v", err, rbErr)
		}
		return result, nil
	}

	// Restart daemons
	u.log("starting daemons")
	if err := u.StartDaemons(); err != nil {
		u.log("failed to start daemons, rolling back", zap.Error(err))
		result.RolledBack = true
		result.RollbackReason = fmt.Sprintf("failed to start daemons: %v", err)
		if rbErr := u.rollback(rollbackPath, dstBinaryPath); rbErr != nil {
			return nil, fmt.Errorf("critical: daemon start failed and rollback failed: start=%w, rollback=%v", err, rbErr)
		}
		return result, nil
	}

	// Verify daemons are healthy
	u.log("verifying daemon health")
	if err := u.VerifyDaemonsHealthy(DefaultHealthCheckTimeout); err != nil {
		u.log("health check failed, rolling back", zap.Error(err))
		result.RolledBack = true
		result.RollbackReason = fmt.Sprintf("health check failed: %v", err)
		if rbErr := u.rollback(rollbackPath, dstBinaryPath); rbErr != nil {
			return nil, fmt.Errorf("critical: health check failed and rollback failed: health=%w, rollback=%v", err, rbErr)
		}
		return result, nil
	}

	u.log("update successful", zap.String("version", newVersion))
	result.Success = true
	return result, nil
}

// StopDaemons stops running watcher and guardian daemons
func (u *Updater) StopDaemons() error {
	entry, err := u.registry.GetAll()
	if err != nil {
		return fmt.Errorf("failed to get registry: %w", err)
	}
	if entry == nil {
		return nil // No daemons registered
	}

	var stopErrors []error

	// Stop watcher
	if entry.WatcherPID > 0 && u.pm.IsRunning(entry.WatcherPID) {
		u.log("stopping watcher", zap.Int("pid", entry.WatcherPID))
		if err := u.signalProcess(entry.WatcherPID, syscall.SIGTERM); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("watcher: %w", err))
		}
	}

	// Stop guardian
	if entry.GuardianPID > 0 && u.pm.IsRunning(entry.GuardianPID) {
		u.log("stopping guardian", zap.Int("pid", entry.GuardianPID))
		if err := u.signalProcess(entry.GuardianPID, syscall.SIGTERM); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("guardian: %w", err))
		}
	}

	// Wait for daemons to stop (they handle SIGTERM gracefully)
	time.Sleep(2 * time.Second)

	// Force kill if still running
	if entry.WatcherPID > 0 && u.pm.IsRunning(entry.WatcherPID) {
		_ = u.pm.Kill(entry.WatcherPID)
	}
	if entry.GuardianPID > 0 && u.pm.IsRunning(entry.GuardianPID) {
		_ = u.pm.Kill(entry.GuardianPID)
	}

	if len(stopErrors) > 0 {
		return fmt.Errorf("errors stopping daemons: %v", stopErrors)
	}
	return nil
}

// StartDaemons starts watcher and guardian daemons
func (u *Updater) StartDaemons() error {
	// Use the daemon package's StartBothDaemons
	// We import it here to avoid circular dependency
	// Actually, we need to spawn the daemons ourselves

	binaryPath := u.execMode.BinaryPath
	config, _ := u.backupManager.GetConfig()
	if config != nil && config.MainBinaryPath != "" {
		binaryPath = config.MainBinaryPath
	}

	// Start watcher
	if err := u.spawnDaemon(binaryPath, "watcher"); err != nil {
		return fmt.Errorf("failed to start watcher: %w", err)
	}

	// Start guardian
	if err := u.spawnDaemon(binaryPath, "guardian"); err != nil {
		return fmt.Errorf("failed to start guardian: %w", err)
	}

	return nil
}

// VerifyDaemonsHealthy waits for both daemons to be running
func (u *Updater) VerifyDaemonsHealthy(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		entry, err := u.registry.GetAll()
		if err == nil && entry != nil {
			watcherAlive := entry.WatcherPID > 0 && u.pm.IsRunning(entry.WatcherPID)
			guardianAlive := entry.GuardianPID > 0 && u.pm.IsRunning(entry.GuardianPID)

			if watcherAlive && guardianAlive {
				return nil // Both daemons running
			}
		}

		time.Sleep(DaemonCheckInterval)
	}

	// Check final state for error message
	entry, _ := u.registry.GetAll()
	if entry == nil {
		return fmt.Errorf("no daemons registered after %v", timeout)
	}

	watcherAlive := u.pm.IsRunning(entry.WatcherPID)
	guardianAlive := u.pm.IsRunning(entry.GuardianPID)

	if !watcherAlive && !guardianAlive {
		return fmt.Errorf("neither daemon running after %v", timeout)
	}
	if !watcherAlive {
		return fmt.Errorf("watcher not running after %v", timeout)
	}
	return fmt.Errorf("guardian not running after %v", timeout)
}

// createRollbackBackup creates a backup specifically for rollback
func (u *Updater) createRollbackBackup(binaryPath string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "appmon-rollback-")
	if err != nil {
		return "", err
	}

	rollbackPath := filepath.Join(tmpDir, "appmon-rollback")
	if err := copyFile(binaryPath, rollbackPath); err != nil {
		os.RemoveAll(tmpDir)
		return "", err
	}

	return rollbackPath, nil
}

// rollback restores the previous binary and restarts daemons
func (u *Updater) rollback(rollbackPath, binaryPath string) error {
	u.log("performing rollback")

	// Stop any running daemons
	_ = u.StopDaemons()

	// Restore binary
	if err := copyFile(rollbackPath, binaryPath); err != nil {
		return fmt.Errorf("failed to restore binary: %w", err)
	}
	_ = os.Chmod(binaryPath, 0755)

	// Update backups with restored binary
	if err := u.backupManager.SetupBackupsWithMode(binaryPath, u.currentVersion, "", u.execMode.Mode); err != nil {
		u.log("warning: failed to update backups during rollback", zap.Error(err))
		// Continue - binary is restored, backups can be fixed manually
	}

	// Restart daemons
	if err := u.StartDaemons(); err != nil {
		return fmt.Errorf("failed to restart daemons after rollback: %w", err)
	}

	// Verify daemons are healthy after rollback
	if err := u.VerifyDaemonsHealthy(DefaultHealthCheckTimeout); err != nil {
		return fmt.Errorf("daemons not healthy after rollback: %w", err)
	}

	return nil
}

// signalProcess sends a signal to a process
func (u *Updater) signalProcess(pid int, sig syscall.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(sig)
}

// spawnDaemon starts a daemon process
func (u *Updater) spawnDaemon(binaryPath, role string) error {
	obfuscator := NewObfuscator()
	daemonName := obfuscator.GenerateName()

	// Use os/exec to spawn detached process
	cmd := &execCmd{
		path: binaryPath,
		args: []string{binaryPath, "daemon", "--role", role, "--name", daemonName},
	}

	return cmd.start()
}

// log logs a message if logger is available
func (u *Updater) log(msg string, fields ...zap.Field) {
	if u.logger != nil {
		u.logger.Info(msg, fields...)
	}
}

// execCmd wraps exec.Command for daemon spawning
type execCmd struct {
	path string
	args []string
}

func (c *execCmd) start() error {
	// Open /dev/null for stdin, stdout, stderr
	devNull, err := os.OpenFile("/dev/null", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open /dev/null: %w", err)
	}
	defer func() {
		if cerr := devNull.Close(); cerr != nil {
			// Log error but don't fail - /dev/null close errors are not critical
			fmt.Fprintf(os.Stderr, "failed to close /dev/null: %v\n", cerr)
		}
	}()

	// Use syscall.ForkExec to spawn a fully detached process
	attr := &syscall.ProcAttr{
		Dir:   "/",
		Env:   os.Environ(),
		Files: []uintptr{devNull.Fd(), devNull.Fd(), devNull.Fd()}, // stdin, stdout, stderr -> /dev/null
		Sys: &syscall.SysProcAttr{
			Setsid: true,
		},
	}

	_, err = syscall.ForkExec(c.path, c.args, attr)
	return err
}

// isRegistryError checks if an error is related to registry access (critical)
// vs signaling errors (non-critical)
func isRegistryError(err error) bool {
	if err == nil {
		return false
	}
	// Registry errors start with "failed to get registry"
	return strings.HasPrefix(err.Error(), "failed to get registry")
}
