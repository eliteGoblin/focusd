// Package app is the composition root: it wires the OS adapter, run-mode
// resolution, path layout, config, state DB, and logger into one App.
// It contains no OS-specific logic — everything OS-bound goes through the
// osadapter interface.
package app

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/eliteGoblin/focusd/platform/internal/bundle"
	"github.com/eliteGoblin/focusd/platform/internal/core/config"
	"github.com/eliteGoblin/focusd/platform/internal/core/logging"
	"github.com/eliteGoblin/focusd/platform/internal/core/plugin"
	"github.com/eliteGoblin/focusd/platform/internal/core/runner"
	"github.com/eliteGoblin/focusd/platform/internal/core/scheduler"
	"github.com/eliteGoblin/focusd/platform/internal/core/snapshot"
	"github.com/eliteGoblin/focusd/platform/internal/core/state"
	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
)

// bundleVerifier adapts the bundle package to both integrity seams: the
// runner's point-of-use integrityVerifier (ADR-0019) and the discoverer's
// integrityGuard (FEATURE 23). The genuine plugin copy embedded in the signed
// platform binary is the trust root — reconciled before the manifest is read
// (discovery) and again immediately before exec (runner).
type bundleVerifier struct{}

func (bundleVerifier) VerifyOrRestore(pluginRoot, subdir string) (bool, string, string, error) {
	return bundle.VerifyOrRestore(pluginRoot, subdir)
}

// IsBundled reports whether subdir is a plugin directory that ships inside the
// signed platform binary — the system-mode discovery allowlist (FEATURE 23).
func (bundleVerifier) IsBundled(subdir string) bool { return bundle.IsBundled(subdir) }

// Options control bootstrap. Zero value is valid: adapter auto-created,
// mode auto-detected, paths from the adapter's default layout.
type Options struct {
	// Adapter overrides the OS adapter (tests inject fakes). nil => real.
	Adapter osadapter.Adapter
	// Config, if non-nil, is the pre-loaded Config the caller wants
	// Bootstrap to use directly. This is the path the platform CLI
	// uses to inject the result of defaultconfig.LoadWithOverrides
	// (embedded default + optional on-disk override merged). When set,
	// ConfigPath is ignored for the config load step.
	Config *config.Config
	// ConfigPath overrides the config file path. "" => adapter default.
	// Ignored when Config is non-nil.
	ConfigPath string
	// StateDBPath overrides the state DB path. "" => adapter default.
	StateDBPath string
	// PluginDir overrides the plugin scan directory. "" => adapter default.
	PluginDir string
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

	pluginDir string // resolved scan dir (override or adapter default)
	logClose  func() error
	// snap is the status-snapshot writer, rooted in the workdir next to
	// state.db. The scheduler and runner mirror every recorded run into it so
	// `platform status` reads run-state from this tiny atomic file instead of
	// contending with the constantly-writing live DB. nil for in-memory DBs.
	snap *snapshot.Store
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

	// Config resolution: prefer the pre-loaded one the CLI handed us
	// (the result of defaultconfig.LoadWithOverrides — embedded
	// defaults merged with optional override file). Fall back to a
	// path-based load if no pre-loaded Config was provided.
	var (
		cfg     *config.Config
		cfgPath string // for the bootstrapped log line only
	)
	if opts.Config != nil {
		cfg = opts.Config
		// Best-effort label for the log: the override path if one was
		// passed (the loader may or may not have actually merged it),
		// otherwise mark the source as the embedded default.
		cfgPath = opts.ConfigPath
		if cfgPath == "" {
			cfgPath = "<embedded default>"
		}
	} else {
		cfgPath = opts.ConfigPath
		if cfgPath == "" {
			// Need a provisional mode just to find the default config
			// path; detection is safe and side-effect free.
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
		c, err := config.Load(cfgPath)
		if err != nil {
			return nil, err
		}
		cfg = c
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

	// Status snapshot lives next to state.db in the workdir. Skip it for an
	// in-memory DB (no real directory) — the snapshot is a no-op there.
	var snap *snapshot.Store
	if dbPath != ":memory:" {
		snap = snapshot.NewStore(filepath.Dir(dbPath))
	}

	log.Info("platform bootstrapped",
		"os", adapter.CurrentOS(), "arch", adapter.CurrentArch(),
		"mode", string(mode), "config", cfgPath, "state_db", dbPath)

	pluginDir := opts.PluginDir
	if pluginDir == "" {
		pd, err := adapter.DefaultPluginDir(mode)
		if err != nil {
			db.Close()
			logClose()
			return nil, fmt.Errorf("resolve plugin dir: %w", err)
		}
		pluginDir = pd
	}

	return &App{
		Adapter:   adapter,
		Mode:      mode,
		Config:    cfg,
		State:     db,
		Log:       log,
		pluginDir: pluginDir,
		logClose:  logClose,
		snap:      snap,
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

// DiscoverPlugins scans the OS plugin directory for this mode, evaluates
// each plugin against host/protocol/privilege/security gates, and syncs
// the result (accepted and rejected) into the inventory.
func (a *App) DiscoverPlugins() ([]plugin.Discovered, error) {
	d := (&plugin.Discoverer{
		GOOS:   a.Adapter.CurrentOS(),
		GOARCH: a.Adapter.CurrentArch(),
		Mode:   a.Mode,
	}).WithIntegrity(bundleVerifier{})
	found, err := d.Discover(a.pluginDir)
	if err != nil {
		return nil, err
	}
	if err := plugin.SyncInventory(a.State, found); err != nil {
		return nil, fmt.Errorf("sync inventory: %w", err)
	}
	for _, p := range found {
		// A tamper repaired at discovery (FEATURE 23, Fix 1): the verify-before-
		// parse check restored one of this plugin's on-disk files to the genuine
		// embedded copy BEFORE the manifest was trusted. Record it as a security
		// event (the runner's point-of-use check would never see it — discovery
		// already restored the genuine files) and log it (redaction-safe: id +
		// sha prefixes only, never a path). Keyed on a synthetic "discovery" job
		// id so it never cross-matches a real job's per-job tamper query.
		if p.Restored {
			a.Log.Warn("plugin tamper repaired at discovery",
				"plugin", rejectedID(p), "want_sha", p.TamperWant, "got_sha", p.TamperGot)
			if rerr := a.State.Events.RecordTamperRepaired("discovery", rejectedID(p), p.TamperWant, p.TamperGot); rerr != nil {
				a.Log.Warn("record discovery tamper event failed", "plugin", rejectedID(p))
			}
		}
		switch {
		case p.OK:
			a.Log.Info("plugin discovered", "id", p.Manifest.ID, "dir", p.Dir)
		case p.Expected:
			// A normal environment mismatch (wrong host, or a plugin this
			// install's mode can't serve). The bundle ships every plugin to
			// every install, so this fires on every clean startup — it is
			// steady state, not a problem. Log at INFO so the whitebox log
			// stays quiet (FEATURE 16). Redaction-safe: id + reason only,
			// Expected-rejection reasons are path-free, but redact anyway for
			// consistency with the WARN sibling + future-proofing.
			a.Log.Info("plugin not servable in this install",
				"plugin", rejectedID(p), "reason", redactPaths(p.Reason))
		default:
			// A genuine defect (corrupt manifest, unsupported protocol,
			// security violation). WARN so the whitebox log flags it.
			// Redaction-safe: log the plugin id (manifest may be nil if the
			// manifest itself failed to parse — fall back to the dir's base
			// name, never the full disguised workdir path) + a path-scrubbed
			// reason (some reasons embed an I/O error string with a path).
			a.Log.Warn("plugin rejected", "plugin", rejectedID(p), "reason", redactPaths(p.Reason))
		}
	}
	return found, nil
}

// BuildScheduler discovers plugins, then registers every enabled job
// whose plugin is runnable. Returns the scheduler and how many jobs were
// registered.
func (a *App) BuildScheduler() (*scheduler.Scheduler, int, error) {
	found, err := a.DiscoverPlugins()
	if err != nil {
		return nil, 0, err
	}
	byID := make(map[string]plugin.Discovered, len(found))
	for _, p := range found {
		if p.Manifest != nil {
			byID[p.Manifest.ID] = p
		}
	}
	run := runner.NewWithMode(a.State, a.Mode).
		WithVerifier(bundleVerifier{}).
		WithLogger(a.Log).
		WithSnapshot(a.snap)
	s := scheduler.New(run, a.State, a.Log, a.Mode).
		WithSnapshot(a.snap)
	n, err := s.Register(a.Config.Jobs, byID)
	if err != nil {
		return nil, 0, err
	}
	// Whole-bundle integrity sweep (ADR-0019 / FEATURE 23): the idle backstop
	// that re-reconciles even idle/disabled plugin binaries the runner's
	// per-scheduled-run point-of-use check never reaches. Cadence is
	// configurable (config.Platform.IntegritySweepInterval; default 1m).
	// ExtractTo is idempotent/churn-free, so this is a no-op when clean.
	pluginDir := a.pluginDir
	if err := s.RegisterIntegritySweep(
		a.Config.Platform.IntegritySweepInterval.Std(),
		func() error {
			_, serr := bundle.ExtractTo(pluginDir)
			return serr
		}); err != nil {
		return nil, 0, err
	}
	return s, n, nil
}

// rejectedID returns a redaction-safe identifier for a rejected plugin: the
// manifest id when available, else the base name of the plugin dir (never
// the full disguised workdir path). A nil manifest happens when the manifest
// itself failed to parse.
func rejectedID(p plugin.Discovered) string {
	if p.Manifest != nil && p.Manifest.ID != "" {
		return p.Manifest.ID
	}
	if p.Dir != "" {
		return filepath.Base(p.Dir)
	}
	return "unknown"
}

// redactPaths scrubs path-like tokens (anything containing a "/") from a
// rejection reason before it reaches the app log, so an I/O error string
// that embeds the disguised workdir can't leak into the whitebox channel.
// Path-like whitespace-delimited tokens (containing a POSIX '/' or a Windows
// '\' separator) are replaced with "<redacted>"; the non-path words of the
// reason (the diagnostic part) are preserved.
func redactPaths(reason string) string {
	fields := strings.Fields(reason)
	for i, f := range fields {
		if strings.ContainsRune(f, '/') || strings.ContainsRune(f, '\\') {
			fields[i] = "<redacted>"
		}
	}
	return strings.Join(fields, " ")
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
