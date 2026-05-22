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
	"syscall"
	"time"

	"github.com/eliteGoblin/focusd/platform/internal/core/app"
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
	cfg := fs.String("config", "", "config.yaml path (default: OS layout)")
	db := fs.String("state-db", "", "state.db path (default: OS layout)")
	pdir := fs.String("plugin-dir", "", "plugin scan dir (default: OS layout)")
	mode := fs.String("mode", "", "force run mode: user|system")
	_ = fs.Parse(args)
	return app.Options{
		ConfigPath:  *cfg,
		StateDBPath: *db,
		PluginDir:   *pdir,
		ForceMode:   osadapter.RunMode(*mode),
	}
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
