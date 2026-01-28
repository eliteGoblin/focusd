// Package daemon implements the watcher and guardian daemons.
package daemon

import (
	"context"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
	"github.com/eliteGoblin/focusd/app_mon/internal/policy"
)

// BackupManager interface for binary self-protection
type BackupManager interface {
	VerifyAndRestore() (restored bool, err error)
	GetMainBinaryPath() string
}

// WatcherConfig holds watcher daemon configuration.
type WatcherConfig struct {
	EnforcementInterval  time.Duration // How often to run enforcement (default 10 min)
	HeartbeatInterval    time.Duration // How often to update heartbeat
	PartnerCheckInterval time.Duration // How often to check guardian
	PlistCheckInterval   time.Duration // How often to check LaunchAgent plist
	BinaryCheckInterval  time.Duration // How often to check binary integrity
	FreedomCheckInterval time.Duration // How often to check Freedom app (default 5s)
}

// DefaultWatcherConfig returns default watcher configuration.
func DefaultWatcherConfig() WatcherConfig {
	return WatcherConfig{
		EnforcementInterval:  policy.DefaultScanInterval, // 10 minutes
		HeartbeatInterval:    30 * time.Second,
		PartnerCheckInterval: 60 * time.Second,
		PlistCheckInterval:   60 * time.Second, // Check plist every minute
		BinaryCheckInterval:  60 * time.Second, // Check binary integrity every minute
		FreedomCheckInterval: 5 * time.Second,  // Check Freedom every 5 seconds (fast restart)
	}
}

// Watcher is the main enforcement daemon.
// It kills blocked processes and deletes blocked paths on a schedule.
// It also monitors the guardian daemon and restarts it if needed.
// It also protects the LaunchAgent plist file, restoring it if deleted.
// It also protects the binary itself, restoring from backup if deleted/corrupted.
// It also protects Freedom app, restarting it if killed.
type Watcher struct {
	config           WatcherConfig
	enforcer         domain.Enforcer
	registry         domain.DaemonRegistry
	processManager   domain.ProcessManager
	launchAgent      domain.LaunchAgentManager
	backupManager    BackupManager
	freedomProtector domain.FreedomProtector
	logger           *zap.Logger
	daemon           domain.Daemon
}

// NewWatcher creates a new watcher daemon.
func NewWatcher(
	config WatcherConfig,
	enforcer domain.Enforcer,
	registry domain.DaemonRegistry,
	pm domain.ProcessManager,
	launchAgent domain.LaunchAgentManager,
	backupManager BackupManager,
	freedomProtector domain.FreedomProtector,
	daemon domain.Daemon,
	logger *zap.Logger,
) *Watcher {
	return &Watcher{
		config:           config,
		enforcer:         enforcer,
		registry:         registry,
		processManager:   pm,
		launchAgent:      launchAgent,
		backupManager:    backupManager,
		freedomProtector: freedomProtector,
		daemon:           daemon,
		logger:           logger,
	}
}

// Run starts the watcher daemon loop.
// This blocks until context is canceled.
func (w *Watcher) Run(ctx context.Context) error {
	// Register ourselves in the registry
	if err := w.registry.Register(w.daemon); err != nil {
		w.logger.Error("failed to register watcher", zap.Error(err))
		return err
	}

	w.logger.Info("watcher daemon started",
		zap.Int("pid", w.daemon.PID),
		zap.String("name", w.daemon.ObfuscatedName))

	// Run enforcement immediately on startup
	w.runEnforcement(ctx)

	// Ensure plist is installed on startup
	w.ensurePlistInstalled()

	// Check binary integrity on startup
	w.ensureBinaryIntegrity()

	// Protect Freedom on startup
	w.protectFreedom()

	// Set up tickers
	enforceTicker := time.NewTicker(w.config.EnforcementInterval)
	heartbeatTicker := time.NewTicker(w.config.HeartbeatInterval)
	partnerCheckTicker := time.NewTicker(w.config.PartnerCheckInterval)
	plistCheckTicker := time.NewTicker(w.config.PlistCheckInterval)
	binaryCheckTicker := time.NewTicker(w.config.BinaryCheckInterval)
	freedomCheckTicker := time.NewTicker(w.config.FreedomCheckInterval)

	defer func() {
		enforceTicker.Stop()
		heartbeatTicker.Stop()
		partnerCheckTicker.Stop()
		plistCheckTicker.Stop()
		binaryCheckTicker.Stop()
		freedomCheckTicker.Stop()
	}()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("watcher daemon stopping")
			return ctx.Err()

		case <-enforceTicker.C:
			w.runEnforcement(ctx)

		case <-heartbeatTicker.C:
			if err := w.registry.UpdateHeartbeat(domain.RoleWatcher); err != nil {
				w.logger.Warn("failed to update heartbeat", zap.Error(err))
			}

		case <-partnerCheckTicker.C:
			w.checkAndRestartGuardian(ctx)

		case <-plistCheckTicker.C:
			w.ensurePlistInstalled()

		case <-binaryCheckTicker.C:
			w.ensureBinaryIntegrity()

		case <-freedomCheckTicker.C:
			w.protectFreedom()
		}
	}
}

// runEnforcement executes all policies.
func (w *Watcher) runEnforcement(ctx context.Context) {
	w.logger.Debug("running enforcement")

	results, err := w.enforcer.Enforce(ctx)
	if err != nil {
		w.logger.Error("enforcement failed", zap.Error(err))
		return
	}

	// Log summary
	var totalKilled, totalDeleted int
	for _, r := range results {
		totalKilled += len(r.KilledPIDs)
		totalDeleted += len(r.DeletedPaths)
	}

	if totalKilled > 0 || totalDeleted > 0 {
		w.logger.Info("enforcement completed",
			zap.Int("processes_killed", totalKilled),
			zap.Int("paths_deleted", totalDeleted))
	}
}

// checkAndRestartGuardian checks if guardian is alive and restarts if needed.
func (w *Watcher) checkAndRestartGuardian(ctx context.Context) {
	alive, err := w.registry.IsPartnerAlive(domain.RoleWatcher)
	if err != nil {
		w.logger.Debug("no guardian registered yet")
		return
	}

	if !alive {
		w.logger.Info("guardian not running, restarting...")
		if err := StartDaemon(domain.RoleGuardian); err != nil {
			w.logger.Error("failed to restart guardian", zap.Error(err))
		} else {
			w.logger.Info("guardian restarted successfully")
		}
	}
}

// ensurePlistInstalled checks if LaunchAgent plist exists and restores if deleted.
// Also checks if content is correct and updates if needed (idempotent).
// This is self-protection: if someone deletes or modifies the plist, we restore it.
func (w *Watcher) ensurePlistInstalled() {
	if w.launchAgent == nil {
		return
	}

	// Get the expected binary path
	var execPath string
	if w.backupManager != nil {
		execPath = w.backupManager.GetMainBinaryPath()
	} else {
		var err error
		execPath, err = os.Executable()
		if err != nil {
			w.logger.Error("failed to get executable path", zap.Error(err))
			return
		}
	}

	if !w.launchAgent.IsInstalled() {
		// Plist missing - restore it
		w.logger.Info("LaunchAgent plist missing, restoring...")
		if err := w.launchAgent.Install(execPath); err != nil {
			w.logger.Error("failed to restore LaunchAgent plist", zap.Error(err))
		} else {
			w.logger.Info("LaunchAgent plist restored successfully")
		}
	} else if w.launchAgent.NeedsUpdate(execPath) {
		// Plist exists but content is wrong - update it
		w.logger.Info("LaunchAgent plist outdated, updating...")
		if err := w.launchAgent.Update(execPath); err != nil {
			w.logger.Error("failed to update LaunchAgent plist", zap.Error(err))
		} else {
			w.logger.Info("LaunchAgent plist updated successfully")
		}
	}
}

// ensureBinaryIntegrity checks if main binary exists and has correct SHA256.
// If not, restores from hidden backup.
func (w *Watcher) ensureBinaryIntegrity() {
	if w.backupManager == nil {
		return
	}

	restored, err := w.backupManager.VerifyAndRestore()
	if err != nil {
		w.logger.Warn("binary integrity check failed", zap.Error(err))
		return
	}

	if restored {
		w.logger.Info("binary was missing/corrupted, restored from backup")
	}
}

// protectFreedom ensures Freedom app is running and login item is present.
// This is "best effort" protection - if Freedom isn't installed, we skip silently.
func (w *Watcher) protectFreedom() {
	if w.freedomProtector == nil {
		return
	}

	actionTaken, err := w.freedomProtector.Protect()
	if err != nil {
		w.logger.Warn("Freedom protection failed", zap.Error(err))
		return
	}

	if actionTaken {
		w.logger.Info("Freedom protection action taken")
	}
}
