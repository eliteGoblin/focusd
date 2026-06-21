// Command platform is the focusd cross-platform protection platform.
//
// It is a thin CLI shell over the composition root (internal/core/app).
// Subcommands:
//
//	platform version              print version
//	platform validate [flags]     bootstrap + report config/state/plugins
//	platform run [flags]          bootstrap + run the scheduler (later phase)
//
// Flags: --config <path>  --state-db <path>  --mode user|system
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/eliteGoblin/focusd/platform/internal/bundle"
	"github.com/eliteGoblin/focusd/platform/internal/core/app"
	"github.com/eliteGoblin/focusd/platform/internal/core/state"
	"github.com/eliteGoblin/focusd/platform/internal/defaultconfig"
	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
	"github.com/eliteGoblin/focusd/platform/internal/status"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	// Convenience: if the first arg is itself a flag (the daemon invokes
	// the platform as `platform --workdir <wd>` with no subcommand),
	// default to `run`. Keeps the daemon→platform contract unchanged
	// while letting `platform validate …` / `platform run …` still work
	// when invoked directly.
	if strings.HasPrefix(cmd, "-") && cmd != "-h" && cmd != "--help" && cmd != "-v" && cmd != "--version" {
		args = os.Args[1:]
		cmd = "run"
	}

	switch cmd {
	case "version", "-v", "--version":
		fmt.Println("focusd-platform", version)
	case "validate":
		os.Exit(runValidate(args))
	case "status":
		os.Exit(runStatus(args))
	case "run":
		os.Exit(runRun(args))
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `focusd-platform — cross-platform protection platform

usage:
  platform version
  platform validate [--config PATH] [--state-db PATH] [--plugin-dir DIR] [--mode user|system]
  platform status   [--workdir DIR] [--config PATH] [--state-db PATH] [--mode user|system] [--json] [--no-color]
  platform run      [--config PATH] [--state-db PATH] [--plugin-dir DIR] [--mode user|system]
`)
}

func parseCommon(name string, args []string) app.Options {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	cfg := fs.String("config", "", "config.yaml path (default: <workdir>/config.yaml or OS layout)")
	db := fs.String("state-db", "", "state.db path (default: <workdir>/state.db or OS layout)")
	pdir := fs.String("plugin-dir", "", "plugin scan dir (default: <platform-binary-dir>/plugins or OS layout)")
	mode := fs.String("mode", "", "force run mode: user|system")
	wd := fs.String("workdir", "", "daemon-managed workdir; derives config/state-db (default: empty = use OS layout)")
	_ = fs.Parse(args)
	opts := app.Options{
		ConfigPath:  *cfg,
		StateDBPath: *db,
		PluginDir:   *pdir,
		ForceMode:   osadapter.RunMode(*mode),
	}
	// --workdir is a convenience for the daemon-managed lifecycle: paths
	// not explicitly set get derived from it, and the bundled plugins
	// are extracted on disk. Config is loaded via the override-merge
	// loader (embedded defaults + optional on-disk override) so new
	// platform releases bring their own defaults without needing to
	// overwrite the user's override.
	if *wd != "" {
		if opts.ConfigPath == "" {
			opts.ConfigPath = filepath.Join(*wd, "config.yaml")
		}
		if opts.StateDBPath == "" {
			opts.StateDBPath = filepath.Join(*wd, "state.db")
		}
		if opts.PluginDir == "" {
			opts.PluginDir = defaultPluginDir(*wd)
		}
		// Workdir + bundle extraction must succeed: a partial start would
		// leave the scheduler with the embedded default jobs but no
		// plugin binaries on disk, silently disabling the protections.
		// Fail loudly instead of pretending to start. (Copilot review.)
		if err := os.MkdirAll(*wd, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "workdir:", err)
			os.Exit(1)
		}
		if _, err := bundle.ExtractTo(opts.PluginDir); err != nil {
			fmt.Fprintln(os.Stderr, "bundle extract:", err)
			os.Exit(1)
		}
		// Load embedded default merged with the optional override file.
		// Pass through opts.Config so app.Bootstrap skips its own
		// path-based load. A malformed override surfaces fail-fast here.
		// Warnings (e.g. plugin-id typos in the override) print to
		// stderr — visible without crashing.
		cfg, warnings, err := defaultconfig.LoadWithOverrides(opts.ConfigPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "config:", err)
			os.Exit(1)
		}
		for _, w := range warnings {
			fmt.Fprintln(os.Stderr, "config warning:", w)
		}
		opts.Config = cfg
	}
	return opts
}

// defaultPluginDir picks where extracted plugins live. We prefer a
// sibling of the platform binary (<binary-dir>/plugins) so different
// platform versions can't fight over the same plugin tree as the
// daemon hot-swaps them. Falls back to <workdir>/plugins if the binary
// path is unknowable.
func defaultPluginDir(workdir string) string {
	exe, err := os.Executable()
	if err == nil && exe != "" {
		return filepath.Join(filepath.Dir(exe), "plugins")
	}
	return filepath.Join(workdir, "plugins")
}

func runValidate(args []string) int {
	a, err := app.Bootstrap(parseCommon("validate", args))
	if err != nil {
		fmt.Fprintln(os.Stderr, "validate failed:", err)
		return 1
	}
	defer a.Close()

	found, derr := a.DiscoverPlugins()
	if derr != nil {
		fmt.Fprintln(os.Stderr, "plugin discovery failed:", derr)
		return 1
	}
	okCount := 0
	for _, p := range found {
		if p.OK {
			okCount++
		}
	}

	sv, _ := a.State.SchemaVersion()
	fmt.Printf("OK  os=%s arch=%s mode=%s schema=v%d jobs=%d services=%d plugins=%d/%d\n",
		a.Adapter.CurrentOS(), a.Adapter.CurrentArch(), a.Mode,
		sv, len(a.Config.Jobs), len(a.Config.Services), okCount, len(found))
	for _, p := range found {
		if !p.OK {
			fmt.Printf("  rejected %s: %s\n", p.Dir, p.Reason)
		}
	}
	return 0
}

// runStatus reports the platform's own health: one line per configured
// job (last-run status + coarse age + verdict) and an overall verdict.
// It is plugin-aware by design — this is the layer the daemon delegates
// plugin detail to (ADR-0012). The output carries NO disguised identifier
// (no paths, labels, or pf anchors), only job ids/statuses/age buckets.
//
// It is deliberately READ-ONLY and side-effect-free: unlike validate/run
// it does NOT bootstrap the app (no plugin extraction, no dir creation, no
// migrations, no path logging). Config is read via the override loader and
// run history via a read-only DB open. If the DB can't be read (missing,
// or a root-owned system DB queried without sudo) it degrades to "no runs
// yet" rather than failing — so a user can always check status.
//
// --json emits machine output; --no-color (or NO_COLOR) suppresses ANSI.
func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	cfgFlag := fs.String("config", "", "config.yaml path")
	dbFlag := fs.String("state-db", "", "state.db path")
	wd := fs.String("workdir", "", "daemon-managed workdir; derives config/state-db paths")
	modeFlag := fs.String("mode", "", "force run mode: user|system")
	jsonOut := fs.Bool("json", false, "emit machine-readable JSON")
	noColor := fs.Bool("no-color", false, "suppress ANSI colour")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	adapter := osadapter.NewAdapter()
	mode := osadapter.RunMode(*modeFlag)
	if mode == "" {
		mode = adapter.DetectRunMode()
	}

	configPath, dbPath := *cfgFlag, *dbFlag
	if *wd != "" {
		if configPath == "" {
			configPath = filepath.Join(*wd, "config.yaml")
		}
		if dbPath == "" {
			dbPath = filepath.Join(*wd, "state.db")
		}
	}
	if configPath == "" {
		configPath, _ = adapter.DefaultConfigPath(mode)
	}
	if dbPath == "" {
		if sd, err := adapter.DefaultStateDir(mode); err == nil {
			dbPath = filepath.Join(sd, "state.db")
		}
	}

	// Config (embedded defaults merged with the optional on-disk override)
	// tells us the job list. This read is harmless and never writes.
	cfg, _, err := defaultconfig.LoadWithOverrides(configPath)
	if err != nil {
		// REDACTION: LoadWithOverrides error strings embed the override path
		// (the disguised workdir). Emit a generic message — never the err.
		fmt.Fprintln(os.Stderr, "status: cannot read configuration")
		return 1
	}
	jobs := make([]status.JobInput, 0, len(cfg.Jobs))
	for _, j := range cfg.Jobs {
		jobs = append(jobs, status.JobInput{ID: j.ID, Enabled: j.Enabled})
	}

	// Run history + tamper history, read-only. On any open failure, degrade
	// to "no runs" (found=false) so status still renders — the jobs just
	// read UNKNOWN, and tamper info is simply absent (never a crash).
	lastRun := func(string) (string, time.Time, bool, error) {
		return "", time.Time{}, false, nil
	}
	var tamperLookup status.TamperLookupFn // nil => no integrity history
	if dbPath != "" {
		if db, derr := state.OpenReadOnly(dbPath); derr == nil {
			defer db.Close()
			lastRun = func(jobID string) (string, time.Time, bool, error) {
				runs, herr := db.Runs.History(jobID, 1)
				if herr != nil || len(runs) == 0 {
					return "", time.Time{}, false, herr
				}
				// A malformed/corrupt StartedAt parses to the year-0 zero time,
				// which would mis-bucket age as ">1h" and lie about staleness
				// (false DEGRADED). Treat an unparseable timestamp as "no run
				// found" → the job reads UNKNOWN ("no runs yet") instead.
				t, perr := time.Parse(time.RFC3339Nano, runs[0].StartedAt)
				if perr != nil {
					return "", time.Time{}, false, nil
				}
				return runs[0].Status, t, true, nil
			}
			// Tamper lookup: a tamper-repaired event newer than the last
			// clean run flips the job to TAMPERED (false-green kill). A query
			// error degrades to "no tamper" rather than failing status.
			tamperLookup = func(jobID string) (time.Time, int, bool) {
				since, count, found, terr := db.Events.TamperSince(jobID, 24*time.Hour)
				if terr != nil {
					return time.Time{}, 0, false
				}
				return since, count, found
			}
		}
	}

	rep := status.Collect(string(mode), jobs, lastRun, tamperLookup, time.Now().UTC())

	color := !*noColor && os.Getenv("NO_COLOR") == ""
	if *jsonOut {
		status.RenderJSON(rep, os.Stdout)
	} else {
		status.RenderText(rep, os.Stdout, color)
	}
	if rep.Overall == status.Healthy || rep.Overall == status.Unknown {
		return 0
	}
	return 1
}

func runRun(args []string) int {
	a, err := app.Bootstrap(parseCommon("run", args))
	if err != nil {
		fmt.Fprintln(os.Stderr, "run failed:", err)
		return 1
	}
	defer a.Close()

	sched, n, serr := a.BuildScheduler()
	if serr != nil {
		fmt.Fprintln(os.Stderr, "scheduler build failed:", serr)
		return 1
	}
	sched.Start()
	a.Log.Info("platform running", "jobs_registered", n)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	a.Log.Info("shutdown requested; draining in-flight jobs")
	stopCtx := sched.Stop()
	select {
	case <-stopCtx.Done():
	case <-time.After(30 * time.Second):
		a.Log.Warn("shutdown drain timed out")
	}
	a.Log.Info("platform stopped")
	return 0
}
