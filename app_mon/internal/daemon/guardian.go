package daemon

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// GuardianConfig holds guardian daemon configuration.
type GuardianConfig struct {
	WatcherCheckInterval time.Duration // How often to check watcher
	HeartbeatInterval    time.Duration // How often to update heartbeat
}

// DefaultGuardianConfig returns default guardian configuration.
func DefaultGuardianConfig() GuardianConfig {
	return GuardianConfig{
		WatcherCheckInterval: 30 * time.Second,
		HeartbeatInterval:    30 * time.Second,
	}
}

// Guardian monitors the watcher daemon and restarts it if killed.
// This is the simpler of the two daemons - its only job is to keep watcher alive.
type Guardian struct {
	config         GuardianConfig
	registry       domain.DaemonRegistry
	processManager domain.ProcessManager
	logger         *zap.Logger
	daemon         domain.Daemon
}

// NewGuardian creates a new guardian daemon.
func NewGuardian(
	config GuardianConfig,
	registry domain.DaemonRegistry,
	pm domain.ProcessManager,
	daemon domain.Daemon,
	logger *zap.Logger,
) *Guardian {
	return &Guardian{
		config:         config,
		registry:       registry,
		processManager: pm,
		daemon:         daemon,
		logger:         logger,
	}
}

// Run starts the guardian daemon loop.
// This blocks until context is canceled.
func (g *Guardian) Run(ctx context.Context) error {
	// Register ourselves in the registry
	if err := g.registry.Register(g.daemon); err != nil {
		g.logger.Error("failed to register guardian", zap.Error(err))
		return err
	}

	g.logger.Info("guardian daemon started",
		zap.Int("pid", g.daemon.PID),
		zap.String("name", g.daemon.ObfuscatedName))

	// Set up tickers
	watcherCheckTicker := time.NewTicker(g.config.WatcherCheckInterval)
	heartbeatTicker := time.NewTicker(g.config.HeartbeatInterval)

	defer func() {
		watcherCheckTicker.Stop()
		heartbeatTicker.Stop()
	}()

	for {
		select {
		case <-ctx.Done():
			g.logger.Info("guardian daemon stopping")
			return ctx.Err()

		case <-watcherCheckTicker.C:
			g.checkAndRestartWatcher(ctx)

		case <-heartbeatTicker.C:
			if err := g.registry.UpdateHeartbeat(domain.RoleGuardian); err != nil {
				g.logger.Warn("failed to update heartbeat", zap.Error(err))
			}
		}
	}
}

// checkAndRestartWatcher checks if watcher is alive and restarts if needed.
func (g *Guardian) checkAndRestartWatcher(ctx context.Context) {
	alive, err := g.registry.IsPartnerAlive(domain.RoleGuardian)
	if err != nil {
		g.logger.Debug("no watcher registered yet")
		return
	}

	if !alive {
		g.logger.Info("watcher not running, restarting...")
		if err := StartDaemon(domain.RoleWatcher); err != nil {
			g.logger.Error("failed to restart watcher", zap.Error(err))
		} else {
			g.logger.Info("watcher restarted successfully")
		}
	}
}
