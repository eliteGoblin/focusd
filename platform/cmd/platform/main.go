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
	"github.com/eliteGoblin/focusd/platform/internal/core/snapshot"
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
  platform status   [--workdir DIR] [--state-db PATH] [--mode user|system] [--json] [--no-color]
  platform run      [--workdir DIR] [--state-db PATH] [--plugin-dir DIR] [--mode user|system]
`)
}

// parseCommon builds app.Options from the shared run/validate flags.
//
// honorConfigFlag gates the dev-only --config path: TRUE only for
// `platform validate`, where a developer points --config at a config file
// to inspect. On the daemon-managed run path it is FALSE — the enforced
// policy is the SIGNED embedded default and nothing else. A config.yaml
// dropped into the workdir is inert (never read), so a weak-moment edit
// cannot loosen enforcement. (config→server is the future direction; the
// embedded signed default is the KISS interim.)
func parseCommon(name string, honorConfigFlag bool, args []string) app.Options {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	cfg := fs.String("config", "", "config.yaml path (dev `validate` only; ignored on the run path)")
	db := fs.String("state-db", "", "state.db path (default: <workdir>/state.db or OS layout)")
	pdir := fs.String("plugin-dir", "", "plugin scan dir (default: <platform-binary-dir>/plugins or OS layout)")
	mode := fs.String("mode", "", "force run mode: user|system")
	wd := fs.String("workdir", "", "daemon-managed workdir; derives state-db/plugin-dir (default: empty = use OS layout)")
	_ = fs.Parse(args)
	opts := app.Options{
		StateDBPath: *db,
		PluginDir:   *pdir,
		ForceMode:   osadapter.RunMode(*mode),
	}
	if honorConfigFlag {
		opts.ConfigPath = *cfg
	}
	// --workdir is a convenience for the daemon-managed lifecycle: state-db
	// and the plugin dir not explicitly set get derived from it, and the
	// bundled plugins are extracted on disk. The enforced policy is the
	// SIGNED embedded default — the workdir's config.yaml is NOT consulted
	// (it was a tamper surface and has been removed).
	if *wd != "" {
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
		// Policy = the signed embedded default ONLY. Load it directly and
		// hand it to Bootstrap via opts.Config (so Bootstrap skips its own
		// path-based load). A malformed embedded default is a build defect
		// → fail fast.
		loaded, err := defaultconfig.Load()
		if err != nil {
			fmt.Fprintln(os.Stderr, "config:", err)
			os.Exit(1)
		}
		opts.Config = loaded
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
	a, err := app.Bootstrap(parseCommon("validate", true, args))
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
	dbFlag := fs.String("state-db", "", "state.db path")
	wd := fs.String("workdir", "", "daemon-managed workdir; derives state-db path")
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

	dbPath := *dbFlag
	if *wd != "" && dbPath == "" {
		dbPath = filepath.Join(*wd, "state.db")
	}
	if dbPath == "" {
		if sd, err := adapter.DefaultStateDir(mode); err == nil {
			dbPath = filepath.Join(sd, "state.db")
		}
	}

	// The job list comes from the SIGNED embedded default — the exact policy
	// the running platform enforces. There is no on-disk override to read (a
	// workdir config.yaml is inert), so status can never disagree with the
	// enforced set. This read is harmless and never writes.
	cfg, err := defaultconfig.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "status: cannot read configuration")
		return 1
	}
	jobs := make([]status.JobInput, 0, len(cfg.Jobs))
	for _, j := range cfg.Jobs {
		jobs = append(jobs, status.JobInput{ID: j.ID, Enabled: j.Enabled})
	}

	// Run-state fast path: read each job's LAST run from the status SNAPSHOT,
	// NOT the live DB. The running platform writes job_runs on every reconcile
	// tick for every plugin; a separate read-only status query against that hot
	// DB kept colliding with the writer's commit lock, and an intermittently
	// failed read surfaced as "no runs yet" → UNKNOWN → the HEALTHY↔UNKNOWN
	// flip. Journal-mode tweaks (WAL, rollback-journal) couldn't survive it —
	// the contention itself was the bug. The reconcile loop mirrors every run
	// into status-snapshot.json (atomic temp+rename), so this read never
	// touches the contended DB. (See internal/core/snapshot.)
	//
	// The snapshot is read ONCE here; the closure just looks jobs up in the
	// already-loaded map. We must NOT repeat the conflation bug:
	//   - snapErr != nil → a transient read/parse failure: every job returns
	//     that error → UNKNOWN for THIS status call only, never a persistent
	//     "never ran" claim.
	//   - missing file (snap == nil, snapErr == nil) → a GENUINE fresh install
	//     with no runs: jobs read UNKNOWN ("warming up").
	//   - present + parsed → the job's recorded last run.
	var snap map[string]snapshot.Entry
	var snapErr error
	if dbPath != "" {
		snap, snapErr = snapshot.Read(filepath.Dir(dbPath))
	}
	lastRun := func(jobID string) (string, time.Time, bool, error) {
		if snapErr != nil {
			return "", time.Time{}, false, snapErr
		}
		e, ok := snap[jobID]
		if !ok {
			return "", time.Time{}, false, nil
		}
		// A zero StartedAt would mis-bucket age as ">1h" and lie about
		// staleness (false DEGRADED). Treat it as "no run found" → UNKNOWN.
		if e.StartedAt.IsZero() {
			return "", time.Time{}, false, nil
		}
		return e.Status, e.StartedAt, true, nil
	}

	// Sweep-health stays on the DB read-only: the integrity-sweep-failed event
	// is written RARELY (only on a wedged sweep), so this query does not
	// contend the way the constant job_runs writes did. A query error or an
	// unopenable DB degrades to "not failing" (no signal) rather than crashing
	// status — it can never produce the run-history flip this fix addresses.
	var sweepFailing status.SweepFailingFn // nil => no sweep-health signal
	if dbPath != "" {
		if db, derr := state.OpenReadOnly(dbPath); derr == nil {
			defer db.Close()
			sweepFailing = func() bool {
				_, failing, serr := db.Events.SweepFailingSince(5 * time.Minute)
				return serr == nil && failing
			}
		}
	}

	rep := status.Collect(string(mode), jobs, lastRun, sweepFailing, time.Now().UTC())

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
	a, err := app.Bootstrap(parseCommon("run", false, args))
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
