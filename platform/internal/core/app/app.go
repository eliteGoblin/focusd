// Package app is the composition root: it wires the OS adapter, run-mode
// resolution, path layout, config, state DB, and logger into one App.
// It contains no OS-specific logic — everything OS-bound goes through the
// osadapter interface.
package app

import (
	"fmt"
	"log/slog"

	"github.com/eliteGoblin/focusd/platform/internal/core/config"
	"github.com/eliteGoblin/focusd/platform/internal/core/logging"
	"github.com/eliteGoblin/focusd/platform/internal/core/state"
	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
)

// Options control bootstrap. Zero value is valid: adapter auto-created,
// mode auto-detected, paths from the adapter's default layout.
type Options struct {
	// Adapter overrides the OS adapter (tests inject fakes). nil => real.
	Adapter osadapter.Adapter
	// ConfigPath overrides the config file path. "" => adapter default.
	ConfigPath string
	// StateDBPath overrides the state DB path. "" => adapter default.
	StateDBPath string
	// ForceMode pins the run mode. "" => config, then adapter detection.
	ForceMode osadapter.RunMode
}

// App holds the wired runtime dependencies.
type App struct {
	Adapter osadapter.Adapter
	Mode    osadapter.RunMode
	Config  *config.Config
	State   *state.DB
	Log     *slog.Logger

	logClose func() error
}

// Bootstrap resolves the runtime in strict order: adapter → run mode →
// paths → config → logger → state DB. Any failure aborts (fail-fast).
func Bootstrap(opts Options) (*App, error) {
	adapter := opts.Adapter
	if adapter == nil {
		adapter = osadapter.NewAdapter()
	}

	// Resolve mode: explicit force > config file > OS detection.
	mode := opts.ForceMode

	// A forced mode is known upfront, so validate privilege before any
	// config I/O — forcing system mode without privilege must fail fast
	// regardless of which config exists (modes are fully isolated).
	if mode != "" {
		if !mode.Valid() {
			return nil, fmt.Errorf("forced run mode %q is invalid", mode)
		}
		if err := guardMode(adapter, mode); err != nil {
			return nil, err
		}
	}

	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		// Need a provisional mode just to find the default config path;
		// detection is safe and side-effect free.
		probe := mode
		if probe == "" {
			probe = adapter.DetectRunMode()
		}
		p, err := adapter.DefaultConfigPath(probe)
		if err != nil {
			return nil, fmt.Errorf("resolve config path: %w", err)
		}
		cfgPath = p
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}

	if mode == "" {
		mode = cfg.Platform.RunMode
	}
	if mode == "" {
		mode = adapter.DetectRunMode()
	}
	if !mode.Valid() {
		return nil, fmt.Errorf("resolved run mode %q is invalid", mode)
	}
	if err := guardMode(adapter, mode); err != nil {
		return nil, err
	}

	logDir, err := adapter.DefaultLogDir(mode)
	if err != nil {
		return nil, fmt.Errorf("resolve log dir: %w", err)
	}
	log, logClose, err := logging.New(cfg.Platform.LogLevel, logDir)
	if err != nil {
		return nil, err
	}

	dbPath := opts.StateDBPath
	if dbPath == "" {
		sd, err := adapter.DefaultStateDir(mode)
		if err != nil {
			logClose()
			return nil, fmt.Errorf("resolve state dir: %w", err)
		}
		dbPath = sd + "/state.db"
	}
	db, err := state.Open(dbPath)
	if err != nil {
		logClose()
		return nil, err
	}

	log.Info("platform bootstrapped",
		"os", adapter.CurrentOS(), "arch", adapter.CurrentArch(),
		"mode", string(mode), "config", cfgPath, "state_db", dbPath)

	return &App{
		Adapter:  adapter,
		Mode:     mode,
		Config:   cfg,
		State:    db,
		Log:      log,
		logClose: logClose,
	}, nil
}

// guardMode enforces that the requested mode is actually available. A
// system-mode request without root is a hard error rather than a silent
// downgrade — the two modes are completely isolated by design.
func guardMode(a osadapter.Adapter, mode osadapter.RunMode) error {
	switch mode {
	case osadapter.ModeSystem:
		if !a.CanRunAsSystem() {
			return fmt.Errorf("system mode requested but process lacks system privilege")
		}
	case osadapter.ModeUser:
		if !a.CanRunAsUser() {
			return fmt.Errorf("user mode requested but unavailable")
		}
	}
	return nil
}

// Close releases the state DB and log file.
func (a *App) Close() error {
	var first error
	if a.State != nil {
		if err := a.State.Close(); err != nil {
			first = err
		}
	}
	if a.logClose != nil {
		if err := a.logClose(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
