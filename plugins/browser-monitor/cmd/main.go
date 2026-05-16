// Command browser-monitor is a focusd job plugin that protects against
// browser distractions on macOS. It is a single self-contained binary:
// the AppleScript automation is embedded via go:embed (no external .sh
// or .applescript files are shipped or required at runtime).
//
// Contract (platform plugin protocol):
//
//	browser-monitor run --config <path-to-job-config.json>
//
//	stdout = JSON result · stderr = diagnostics
//	exit 0 = ok · 1 = controlled failure (a kill failed) · 2+ = runtime error
//
// macOS note: the binary invokes /usr/bin/osascript, so macOS may
// require Automation (and sometimes Accessibility) permission to be
// granted to the process that launches this plugin. See README.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/eliteGoblin/focusd/plugins/browser-monitor/internal/guard"
)

var version = "dev"

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

func run(args []string) int {
	if len(args) >= 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Println("browser-monitor", version)
		return 0
	}
	if len(args) < 1 || args[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: browser-monitor run --config <path>")
		return 2
	}

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to resolved job config JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	blocklist, err := loadBlocklist(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		emit(result{Status: "error", Message: err.Error()})
		return 2
	}

	g := guard.New(blocklist, guard.RealListTabs, guard.RealKill)
	return report(g)
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

// loadBlocklist resolves the effective blocklist: config.blocklist
// (replaces the default) and config.extra_blocklist (appended). No path
// or no keys => the embedded DefaultBlocklist.
func loadBlocklist(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
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
