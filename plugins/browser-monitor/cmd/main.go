// Command browser-monitor is a focusd browser-distraction guard for macOS.
// It is a single self-contained binary with TWO entry tiers, selected by argv
// alone (never by probing launchd/the filesystem):
//
//	PLUGIN MODE (platform-supervised — UNCHANGED, never self-installs/heals):
//	  browser-monitor run --config <path>   scan once; kill browsers on a blocklisted tab
//	    stdout = JSON result · stderr = diagnostics
//	    exit 0 = ok · 1 = controlled failure (a kill failed) · 2+ = runtime error
//
//	STANDALONE SELF-DAEMON (best-effort, user-mode; no platform required —
//	FEATURE 27; see internal/selfdaemon):
//	  browser-monitor daemon-install        install self-healing LaunchAgent + cron
//	  browser-monitor self-tick             heal-then-scan (invoked by the schedule)
//	  browser-monitor daemon-uninstall      remove the self-daemon
//
// The AppleScript automation is embedded via go:embed (no external files at
// runtime). macOS note: the binary invokes /usr/bin/osascript, so macOS may
// require Automation (and sometimes Accessibility) permission to be granted to
// the process that launches this plugin. See README.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/eliteGoblin/focusd/plugins/browser-monitor/internal/guard"
	"github.com/eliteGoblin/focusd/plugins/browser-monitor/internal/selfdaemon"
)

var version = "dev"

const usage = `browser-monitor — macOS browser-distraction guard

plugin mode (platform-supervised):
  browser-monitor run --config <path>   scan once; kill browsers on a blocklisted tab

standalone self-daemon (best-effort, user-mode; no platform required):
  browser-monitor daemon-install        install self-healing LaunchAgent + cron fallback
  browser-monitor self-tick             heal-then-scan (invoked by the schedule)
  browser-monitor daemon-uninstall      remove the self-daemon

  browser-monitor version`

// agentFactory builds the standalone self-daemon Agent. A var so tests can
// route the daemon-* subcommands to a temp-dir agent instead of the real
// launchd/cron.
var agentFactory = selfdaemon.DefaultAgent

// jobInput mirrors the platform's plugin.JobInput. Duplicated so the
// plugin stays an independently released binary (no platform import).
type jobInput struct {
	JobID    string         `json:"job_id"`
	PluginID string         `json:"plugin_id"`
	Config   map[string]any `json:"config"`
}

type result struct {
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func main() { os.Exit(run(os.Args[1:])) }

// run dispatches on argv only — mode is never inferred from launchd or the
// filesystem. The `run` (plugin) path is unchanged from the original binary.
func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Println("browser-monitor", version)
		return 0
	case "run":
		return runPlugin(args)
	case "daemon-install":
		return daemonInstall()
	case "self-tick":
		return selfTick()
	case "daemon-uninstall":
		return daemonUninstall()
	default:
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
}

// runPlugin is the platform-supervised plugin mode: parse the job config, run a
// single guard pass, emit the JSON result. It NEVER installs or heals anything.
func runPlugin(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to resolved job config JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	raw, err := readJobConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		emit(result{Status: "error", Message: err.Error()})
		return 2
	}
	blocklist, err := loadBlocklist(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		emit(result{Status: "error", Message: err.Error()})
		return 2
	}

	g := guard.New(blocklist, guard.RealListTabs, guard.RealKill)
	return report(g)
}

// daemonInstall deploys the standalone self-daemon.
//
// Coexistence (FEATURE 27): we deliberately do NOT probe launchd or the
// disguised platform paths to "detect" the enforced platform — that would mean
// enumerating exactly the identifiers the design keeps hidden. If the enforced
// platform already runs browser-monitor here, this standalone daemon is simply
// redundant and a double browser-quit is harmless (idempotent). So we advise,
// and proceed best-effort.
func daemonInstall() int {
	fmt.Fprintln(os.Stderr, "note: if the focusd enforced platform is already active on this machine,")
	fmt.Fprintln(os.Stderr, "      it already runs browser-monitor; this standalone daemon is then redundant")
	fmt.Fprintln(os.Stderr, "      (harmless). It is intended for machines WITHOUT the enforced platform.")
	a, err := agentFactory()
	if err != nil {
		fmt.Fprintln(os.Stderr, "install failed:", err)
		return 2
	}
	if err := a.Install(); err != nil {
		fmt.Fprintln(os.Stderr, "install failed:", err)
		return 2
	}
	fmt.Println("browser-monitor standalone self-daemon installed (LaunchAgent + cron fallback + hidden self-copies).")
	return 0
}

func selfTick() int {
	a, err := agentFactory()
	if err != nil {
		fmt.Fprintln(os.Stderr, "self-tick:", err)
		return 2
	}
	return a.Tick()
}

func daemonUninstall() int {
	a, err := agentFactory()
	if err != nil {
		fmt.Fprintln(os.Stderr, "uninstall failed:", err)
		return 2
	}
	if err := a.Uninstall(); err != nil {
		fmt.Fprintln(os.Stderr, "uninstall failed:", err)
		return 2
	}
	fmt.Println("browser-monitor standalone self-daemon removed.")
	return 0
}

// report runs a scan and maps the outcome to the plugin contract:
// runtime error => 2, some kill failed => 1 (controlled), else 0.
// Split from run() so the result/exit mapping is unit-testable without
// invoking osascript.
func report(g *guard.Guard) int {
	out, err := g.Scan()
	if err != nil {
		// Typically: not macOS, or Automation permission not granted.
		fmt.Fprintln(os.Stderr, "scan error:", err)
		emit(result{
			Status:  "error",
			Message: err.Error(),
			Details: map[string]any{"hint": "grant Automation permission to the launching app on macOS"},
		})
		return 2
	}

	details := map[string]any{
		"checked":      out.Checked,
		"blocked":      out.Blocked,
		"killed":       out.Killed,
		"killed_count": len(out.Killed),
	}
	if len(out.Failed) > 0 {
		details["failed"] = out.Failed
		emit(result{
			Status: "failed",
			Message: fmt.Sprintf("checked %d tabs, %d blocked, %d killed, %d kill(s) failed",
				out.Checked, len(out.Blocked), len(out.Killed), len(out.Failed)),
			Details: details,
		})
		return 1
	}
	emit(result{
		Status: "ok",
		Message: fmt.Sprintf("checked %d tabs, %d blocked, %d browser(s) killed",
			out.Checked, len(out.Blocked), len(out.Killed)),
		Details: details,
	})
	return 0
}

// readJobConfig returns the job-config JSON bytes: from --config <path>
// (compat) when set, else drained from stdin (the disguised path — the
// config path never appears in this process's argv). Empty/absent => nil
// (grounded defaults). HF4 (FEATURE 24).
func readJobConfig(cfgPath string) ([]byte, error) {
	if cfgPath != "" {
		return os.ReadFile(cfgPath)
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, fmt.Errorf("read stdin config: %w", err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil // no config → defaults
	}
	return b, nil
}

// loadBlocklist resolves the effective blocklist: config.blocklist
// (replaces the default) and config.extra_blocklist (appended). Empty/nil
// raw or no keys => the embedded DefaultBlocklist.
func loadBlocklist(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var in jobInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("parse config JSON: %w", err)
	}

	base, err := stringList(in.Config, "blocklist")
	if err != nil {
		return nil, err
	}
	extra, err := stringList(in.Config, "extra_blocklist")
	if err != nil {
		return nil, err
	}

	if len(base) == 0 && len(extra) == 0 {
		return nil, nil // use embedded default
	}
	if len(base) == 0 {
		base = append([]string(nil), guard.DefaultBlocklist...)
	}
	return append(base, extra...), nil
}

func stringList(cfg map[string]any, key string) ([]string, error) {
	v, ok := cfg[key]
	if !ok {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("config.%s must be a list of strings", key)
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("config.%s entries must be strings", key)
		}
		out = append(out, s)
	}
	return out, nil
}

func emit(r result) {
	b, _ := json.Marshal(r)
	fmt.Println(string(b))
}
