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
	"github.com/eliteGoblin/focusd/platform/internal/defaultconfig"
	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
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
		_ = os.MkdirAll(*wd, 0o755)
		_, _ = bundle.ExtractTo(opts.PluginDir)
		// Load embedded default merged with the optional override file.
		// Pass through opts.Config so app.Bootstrap skips its own
		// path-based load. A malformed override surfaces fail-fast here.
		cfg, err := defaultconfig.LoadWithOverrides(opts.ConfigPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "config:", err)
			os.Exit(1)
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
