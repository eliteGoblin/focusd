// Command kill-steam is a focusd job plugin that terminates Steam/Dota2
// processes. It follows the platform plugin contract:
//
//	kill-steam run --config <path-to-job-config.json>
//
// Input  : JSON file {job_id, plugin_id, config:{process_names?:[...]}}
// Output : JSON result on stdout, diagnostics on stderr
// Exit   : 0 success · 1 controlled failure (some kills failed) · 2 error
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/eliteGoblin/focusd/plugins/kill-steam/internal/killer"
)

var version = "dev"

// jobInput mirrors the platform's plugin.JobInput. Duplicated here so
// the plugin stays an independently released binary (no platform import).
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
		fmt.Println("kill-steam", version)
		return 0
	}
	if len(args) < 1 || args[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: kill-steam run --config <path>")
		return 2
	}

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to resolved job config JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	names, err := loadNames(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		emit(result{Status: "error", Message: err.Error()})
		return 2
	}

	out, err := killer.New(names).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "kill error:", err)
		emit(result{Status: "error", Message: err.Error()})
		return 2
	}

	res := result{
		Status:  "ok",
		Message: fmt.Sprintf("scanned %d, killed %d", out.Scanned, out.KilledCount()),
		Details: map[string]any{
			"scanned":      out.Scanned,
			"killed_count": out.KilledCount(),
			"killed_pids":  out.KilledPIDs,
		},
	}
	if len(out.Failed) > 0 {
		res.Status = "failed"
		res.Message = fmt.Sprintf("killed %d, %d failed", out.KilledCount(), len(out.Failed))
		res.Details["failed"] = out.Failed
		emit(res)
		return 1 // controlled failure
	}
	emit(res)
	return 0
}

// loadNames reads optional config.process_names; "" path => defaults.
func loadNames(path string) ([]string, error) {
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
	v, ok := in.Config["process_names"]
	if !ok {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("config.process_names must be a list of strings")
	}
	names := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("config.process_names entries must be strings")
		}
		names = append(names, s)
	}
	return names, nil
}

func emit(r result) {
	b, _ := json.Marshal(r)
	fmt.Println(string(b))
}
