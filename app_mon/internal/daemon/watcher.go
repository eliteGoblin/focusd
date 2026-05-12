// Package daemon implements the watcher and guardian daemons.
package daemon

import (
	"context"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
	"github.com/eliteGoblin/focusd/app_mon/internal/infra"
	"github.com/eliteGoblin/focusd/app_mon/internal/policy"
)

// BackupManager interface for binary self-protection
type BackupManager interface {
	VerifyAndRestore() (restored bool, err error)
	GetMainBinaryPath() string
}

// WatcherConfig holds watcher daemon configuration.
type WatcherConfig struct {
	EnforcementInterval  time.Duration // Full enforcement tick (kill + brew uninstall + path delete). Heavy; runs every 60s.
	QuickKillInterval    time.Duration // Fast process-kill tick. Cheap (~100ms); runs every 10s to shrink the launch-to-kill window.
	HeartbeatInterval    time.Duration // How often to update heartbeat
	PartnerCheckInterval time.Duration // How often to check guardian
	PlistCheckInterval   time.Duration // How often to check LaunchAgent plist + /etc/hosts blocklist
	BinaryCheckInterval  time.Duration // How often to check binary integrity
	FreedomCheckInterval time.Duration // How often to check Freedom app (default 5s)
}

// DefaultWatcherConfig returns default watcher configuration.
//
// Cadence is split into two enforcement ticks so launching a blocked
// app between heavy scans doesn't grant a multi-minute network window:
//   - QuickKillInterval (10s): cheap FindByName+Kill loop. Catches a
//     freshly-launched Steam within ~10s.
//   - EnforcementInterval (60s): full scan including brew uninstall +
//     path delete. Removes the binary so a reinstall isn't free.
func DefaultWatcherConfig() WatcherConfig {
	return WatcherConfig{
		EnforcementInterval:  60 * time.Second,
		QuickKillInterval:    10 * time.Second,
		HeartbeatInterval:    30 * time.Second,
		PartnerCheckInterval: 60 * time.Second,
		PlistCheckInterval:   60 * time.Second, // Check plist + hosts blocklist every minute
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
// It also maintains a DNS blocklist in /etc/hosts so blocked domains
// fail to resolve regardless of which application asks (Steam ignores
// the system HTTP proxy, so DNS-layer is the only reliable network block).
type Watcher struct {
	config           WatcherConfig
	enforcer         domain.Enforcer
	registry         domain.DaemonRegistry
	processManager   domain.ProcessManager
	launchAgent      domain.LaunchAgentManager
	backupManager    BackupManager
	freedomProtector domain.FreedomProtector
	hostsManager     *infra.HostsManager
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
	hostsManager *infra.HostsManager,
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
		hostsManager:     hostsManager,
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

	// Sweep stale relocated daemon binaries on startup
	w.sweepStaleRelocations()

	// Reap any daemon processes running from our relocator dir that aren't
	// in the registry. This is how we recover from update rollbacks,
	// crashed parents, or racing spawns — the encrypted registry is the
	// single source of truth for which daemons are "ours".
	w.reapOrphans()

	// Install / refresh DNS blocklist in /etc/hosts on startup. This
	// is the first line of defense against Steam-style apps that ignore
	// the system HTTP proxy: even when launched they can't resolve
	// their servers.
	w.ensureHostsBlocklist()

	// Protect Freedom on startup
	w.protectFreedom()

	// Set up tickers
	enforceTicker := time.NewTicker(w.config.EnforcementInterval)
	quickKillTicker := time.NewTicker(w.config.QuickKillInterval)
	heartbeatTicker := time.NewTicker(w.config.HeartbeatInterval)
	partnerCheckTicker := time.NewTicker(w.config.PartnerCheckInterval)
	plistCheckTicker := time.NewTicker(w.config.PlistCheckInterval)
	binaryCheckTicker := time.NewTicker(w.config.BinaryCheckInterval)
	freedomCheckTicker := time.NewTicker(w.config.FreedomCheckInterval)

	defer func() {
		enforceTicker.Stop()
		quickKillTicker.Stop()
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

		case <-quickKillTicker.C:
			w.runQuickKill(ctx)

		case <-heartbeatTicker.C:
			if err := w.registry.UpdateHeartbeat(domain.RoleWatcher); err != nil {
				w.logger.Warn("failed to update heartbeat", zap.Error(err))
			}

		case <-partnerCheckTicker.C:
			w.checkAndRestartGuardian(ctx)

		case <-plistCheckTicker.C:
			w.ensurePlistInstalled()
			w.ensureHostsBlocklist()

		case <-binaryCheckTicker.C:
			w.ensureBinaryIntegrity()
			w.sweepStaleRelocations()
			w.reapOrphans()

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
//
// The plist's ProgramArguments[0] points to a relocated "launch stub" with a
// randomized basename so Login Items shows an obfuscated name rather than
// "appmon". If the stub can't be (re)built, fall back to the main binary
// path — auto-start still works, just without that layer of obfuscation.
func (w *Watcher) ensurePlistInstalled() {
	if w.launchAgent == nil {
		return
	}

	// Resolve the canonical main-binary path.
	var mainBinary string
	if w.backupManager != nil {
		mainBinary = w.backupManager.GetMainBinaryPath()
	} else {
		var err error
		mainBinary, err = os.Executable()
		if err != nil {
			w.logger.Error("failed to get executable path", zap.Error(err))
			return
		}
	}

	// Prefer the launch stub so Login Items doesn't display "appmon".
	execPath := mainBinary
	if store, ok := w.registry.(domain.SecretStore); ok {
		if stub, err := infra.EnsureLaunchStub(mainBinary, infra.GetRealUserHome(), store); err == nil {
			execPath = stub
		} else {
			w.logger.Warn("failed to ensure launch stub, falling back to main binary",
				zap.Error(err))
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

// sweepStaleRelocations removes orphaned binary copies from the relocator
// cache directory. Each daemon spawn creates a fresh copy/link, so repeated
// crash-restart cycles accumulate stale files. The watcher's own exec path
// and the launch stub are explicitly preserved; everything else older than
// minAge is removed. Unlinking a binary while a process holds it open is
// safe on macOS — the kernel keeps the inode alive until the process exits,
// and peer-restart always spawns through a fresh relocation anyway.
func (w *Watcher) sweepStaleRelocations() {
	relocator := infra.NewRelocator(infra.GetRealUserHome())
	keep := []string{}

	if exe, err := os.Executable(); err == nil {
		keep = append(keep, exe)
	}
	if store, ok := w.registry.(domain.SecretStore); ok {
		if stub, err := store.GetSecret(infra.SecretKeyLaunchStub); err == nil && stub != "" {
			keep = append(keep, stub)
		}
	}

	if err := relocator.CleanStale(keep, 2*time.Minute); err != nil {
		w.logger.Debug("relocation sweep failed", zap.Error(err))
	}
}

// runQuickKill runs the fast process-kill loop across all policies.
// Skips brew uninstall + path deletion — those run on the slower
// EnforcementInterval. Purpose: shrink the time between a blocked app
// launching and being killed from ~60s to ~10s, so apps like Steam
// don't get a chance to start network transfers before they die.
func (w *Watcher) runQuickKill(ctx context.Context) {
	if _, err := w.enforcer.EnforceKillOnly(ctx); err != nil {
		w.logger.Warn("quick-kill failed", zap.Error(err))
	}
}

// ensureHostsBlocklist installs / refreshes the appmon-managed section
// of /etc/hosts so blocked domains can't be resolved. In user mode the
// daemon lacks permission to write /etc/hosts; the resulting EACCES is
// logged at Debug and the call returns — DNS blocking is a system-mode-
// only layer, not a regression for user-mode users.
//
// On any successful write, the macOS DNS cache is flushed so the new
// entries take effect immediately rather than after the resolver's TTL.
func (w *Watcher) ensureHostsBlocklist() {
	if w.hostsManager == nil {
		return
	}
	changed, err := w.hostsManager.EnsureBlocklist(policy.DefaultDNSBlocklist)
	if err != nil {
		if os.IsPermission(err) {
			w.logger.Debug("hosts blocklist skipped (need root)", zap.Error(err))
			return
		}
		w.logger.Warn("hosts blocklist write failed", zap.Error(err))
		return
	}
	if changed {
		w.logger.Info("hosts blocklist updated",
			zap.Int("entries", len(policy.DefaultDNSBlocklist)))
		if err := w.hostsManager.FlushDNSCache(); err != nil {
			w.logger.Debug("DNS cache flush failed (entries still apply on next lookup)",
				zap.Error(err))
		}
	}
}

// reapOrphans kills any daemon process whose PID is not recorded in the
// encrypted registry as a live daemon. Two sources are checked:
//   - relocator cache dir (current daemons running under obfuscated
//     basenames)
//   - legacy "appmon"-named daemons with `daemon --role` argv (pre-
//     relocation builds; they don't appear in the cache dir, so the first
//     pass misses them)
//
// The encrypted registry is the source of truth: anything else is an
// orphan from a failed update rollback, a racing spawn, or a stale
// pre-upgrade session. Reaping keeps peer-restart converging on exactly
// one watcher + one guardian.
//
// Safe at high frequency: we never kill our own PID or registered partner.
func (w *Watcher) reapOrphans() {
	keep := map[int]struct{}{os.Getpid(): {}}
	if entry, err := w.registry.GetAll(); err == nil && entry != nil {
		if entry.WatcherPID > 0 {
			keep[entry.WatcherPID] = struct{}{}
		}
		if entry.GuardianPID > 0 {
			keep[entry.GuardianPID] = struct{}{}
		}
	}

	candidates := map[int]string{}
	relocator := infra.NewRelocator(infra.GetRealUserHome())
	if pids, err := relocator.FindProcessesUsingDir(); err == nil {
		for _, pid := range pids {
			candidates[pid] = "relocated"
		}
	} else {
		w.logger.Debug("orphan reap: relocator dir scan failed", zap.Error(err))
	}
	if pids, err := infra.FindLegacyAppmonDaemons(); err == nil {
		for _, pid := range pids {
			candidates[pid] = "legacy-appmon"
		}
	} else {
		w.logger.Debug("orphan reap: legacy-daemon scan failed", zap.Error(err))
	}

	for pid, source := range candidates {
		if _, ok := keep[pid]; ok {
			continue
		}
		w.logger.Info("reaping orphan daemon",
			zap.Int("pid", pid),
			zap.String("source", source))
		_ = w.processManager.Kill(pid)
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
