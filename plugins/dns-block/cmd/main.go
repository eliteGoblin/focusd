// Command dns-block is a focusd job plugin that reconciles /etc/hosts to
// contain a canonical 0.0.0.0 blocklist for the embedded domains.
//
// Contract (same as every focusd job plugin):
//
//	dns-block run --config <path-to-job-config.json>
//	  reads JSON  {job_id, plugin_id, config}
//	  prints      {status, message, details} to stdout
//	  exit code   0 ok · 1 controlled failure · 2 plugin error
//
// The job config can override the embedded blocklist with an explicit
// "hosts": ["…", …] list (useful for tests/future server-driven mode).
// If absent, the plugin uses the embedded data/*.txt.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/eliteGoblin/focusd/plugins/dns-block/internal/reconciler"
)

var version = "dev"

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
		fmt.Println("dns-block", version)
		return 0
	}
	if len(args) < 1 || args[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: dns-block run --config <path>")
		return 2
	}

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to resolved job config JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	raw, err := readJobConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		emit(result{Status: "error", Message: err.Error()})
		return 2
	}
	hosts, err := loadHostsOverride(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		emit(result{Status: "error", Message: err.Error()})
		return 2
	}

	r := &reconciler.Reconciler{Domains: hosts}
	out, rerr := r.Reconcile()
	if rerr != nil {
		fmt.Fprintln(os.Stderr, "reconcile:", rerr)
		emit(result{Status: "error", Message: rerr.Error()})
		return 2
	}
	emit(result{
		Status:  "ok",
		Message: fmt.Sprintf("%s (%d domains)", out.Reason, out.Domains),
		Details: map[string]any{
			"changed": out.Changed,
			"domains": out.Domains,
			"reason":  out.Reason,
		},
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

// loadHostsOverride returns the "hosts" list from the job config if
// present, otherwise nil (which makes the reconciler use the embedded
// blocklist). Empty/nil raw is tolerated (run with no config = embedded).
func loadHostsOverride(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var in jobInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("parse config JSON: %w", err)
	}
	v, ok := in.Config["hosts"]
	if !ok {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("config.hosts must be an array")
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		s, ok := x.(string)
		if !ok {
			return nil, fmt.Errorf("config.hosts must be strings")
		}
		out = append(out, s)
	}
	return out, nil
}

// emit mirrors plugins/kill-steam's pattern: marshal + Println, so the
// stdout format is identical across sibling plugins.
func emit(r result) {
	b, _ := json.Marshal(r)
	fmt.Println(string(b))
}
