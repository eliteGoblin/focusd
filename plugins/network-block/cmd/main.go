// Command network-block is a focusd job plugin that reconciles a pf
// table with the live A-record set for a configured list of domains.
//
// Contract (focusd platform plugin protocol v1):
//
//	network-block run --config <path-to-job-config.json>
//
// Input  : JSON file {job_id, plugin_id, config:{anchor,table,domains,resolver}}
// Output : structured JSON {status,message,details} on stdout
// Stderr : per-op diagnostics (add/delete/resolve errors)
// Exit   : 0 success
//
//	1 controlled failure (DoH unreachable, pfctl missing, sudo refused)
//	2 plugin error (bad args, bad config)
//
// What this is NOT: a crypto barrier. The user can wipe the table with
// `sudo pfctl -F all`. This plugin is a *reconciler* that closes the
// drift gap between the live Steam IPs (rotated hourly by Akamai/CF)
// and a user-maintained pf anchor.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/eliteGoblin/focusd/plugins/network-block/internal/dns"
	"github.com/eliteGoblin/focusd/plugins/network-block/internal/pfctl"
	"github.com/eliteGoblin/focusd/plugins/network-block/internal/reconciler"
)

var version = "dev"

// jobInput mirrors the platform's plugin.JobInput envelope. Duplicated
// per-plugin so this binary stays self-contained (no platform import).
type jobInput struct {
	JobID    string         `json:"job_id"`
	PluginID string         `json:"plugin_id"`
	Config   map[string]any `json:"config"`
}

// pluginConfig is the typed shape of jobInput.Config for this plugin.
type pluginConfig struct {
	Anchor   string
	Table    string
	Domains  []string
	Resolver string
}

type result struct {
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func main() { os.Exit(run(os.Args[1:])) }

// run is the contract entrypoint; split from main so tests can drive
// it with controlled args and inspect the exit code.
func run(args []string) int {
	if len(args) >= 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Println("network-block", version)
		return 0
	}
	if len(args) < 1 || args[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: network-block run --config <path>")
		return 2
	}

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to resolved job config JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		emit(result{Status: "error", Message: err.Error()})
		return 2
	}

	return runWithDeps(cfg, nil, nil)
}

// runWithDeps lets the CLI integration tests inject fake resolver and
// pfctl runners without spinning up the network or shelling out. Pass
// nil for production defaults.
func runWithDeps(cfg pluginConfig, resolver reconciler.Resolver, pf reconciler.PfTable) int {
	if resolver == nil {
		rv, err := dns.NewResolver(cfg.Resolver)
		if err != nil {
			fmt.Fprintln(os.Stderr, "resolver:", err)
			emit(result{Status: "failed", Message: err.Error()})
			return 1
		}
		resolver = rv
	}
	if pf == nil {
		pf = pfctl.NewRunner(cfg.Anchor, cfg.Table)
	}

	r := &reconciler.Reconciler{
		Resolver: resolver,
		Pf:       pf,
		Domains:  cfg.Domains,
		Logger:   os.Stderr,
	}

	// Bound the entire pass so a hung DoH or pfctl can't outrun the
	// scheduler's own job timeout. Picks 45s as a budget under the
	// configured 60s job timeout in defaultconfig/config.yaml.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	out, err := r.Reconcile(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reconcile:", err)
		emit(result{
			Status:  "failed",
			Message: err.Error(),
			Details: map[string]any{
				"added":         out.Added,
				"removed":       out.Removed,
				"current_count": out.CurrentCount,
			},
		})
		return 1
	}

	emit(result{
		Status:  "ok",
		Message: fmt.Sprintf("added=%d removed=%d", len(out.Added), len(out.Removed)),
		Details: map[string]any{
			"added":         out.Added,
			"removed":       out.Removed,
			"current_count": out.CurrentCount,
		},
	})
	return 0
}

// loadConfig parses the job-config JSON file and validates it. Returns
// an error suitable for exit-code-2 (plugin error) reporting.
func loadConfig(path string) (pluginConfig, error) {
	if path == "" {
		return pluginConfig{}, fmt.Errorf("--config is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return pluginConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var in jobInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return pluginConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}

	cfg := pluginConfig{}
	if v, ok := in.Config["anchor"].(string); ok {
		cfg.Anchor = v
	}
	if v, ok := in.Config["table"].(string); ok {
		cfg.Table = v
	}
	if v, ok := in.Config["resolver"].(string); ok {
		cfg.Resolver = v
	}
	if v, ok := in.Config["domains"].([]any); ok {
		for _, x := range v {
			s, ok := x.(string)
			if !ok {
				return pluginConfig{}, fmt.Errorf("config.domains must be strings")
			}
			cfg.Domains = append(cfg.Domains, s)
		}
	}

	if cfg.Anchor == "" {
		return cfg, fmt.Errorf("config.anchor is required")
	}
	if cfg.Table == "" {
		return cfg, fmt.Errorf("config.table is required")
	}
	if len(cfg.Domains) == 0 {
		return cfg, fmt.Errorf("config.domains is required and non-empty")
	}
	if cfg.Resolver == "" {
		return cfg, fmt.Errorf("config.resolver is required")
	}
	if len(cfg.Resolver) < 8 || cfg.Resolver[:8] != "https://" {
		return cfg, fmt.Errorf("config.resolver must start with https://")
	}
	return cfg, nil
}

// emit writes the result JSON to stdout. A blank stdout makes the
// platform runner treat exit 0 as "exit 0 but invalid result JSON" and
// trigger a retry storm, so we MUST emit valid JSON even on Marshal
// failure.
func emit(r result) {
	b, err := json.Marshal(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, "emit: marshal:", err)
		fmt.Println(`{"status":"error","message":"emit marshal failed"}`)
		return
	}
	fmt.Println(string(b))
}
