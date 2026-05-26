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
	"github.com/eliteGoblin/focusd/plugins/kill-steam/internal/uninstaller"
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

	// Phase 1 — kill any live Steam/Dota processes (the existing logic).
	out, err := killer.New(names).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "kill error:", err)
		emit(result{Status: "error", Message: err.Error()})
		return 2
	}

	// Phase 2 — if Steam.app exists on disk, full auto-uninstall:
	// remove the app + every user's Steam appdata + caches + launchd
	// helper. Cheap when Steam is absent (one os.Stat → return).
	un := (&uninstaller.Reconciler{}).Reconcile()

	res := result{
		Status: "ok",
		Message: fmt.Sprintf("scanned=%d killed=%d uninstall_detected=%v removed=%d",
			out.Scanned, out.KilledCount(), un.Detected, len(un.Removed)),
		Details: map[string]any{
			"scanned":            out.Scanned,
			"killed_count":       out.KilledCount(),
			"killed_pids":        out.KilledPIDs,
			"uninstall_detected": un.Detected,
			"uninstall_removed":  un.Removed,
			"uninstall_errors":   un.Errors,
			"uninstall_reason":   un.Reason,
		},
	}
	if len(out.Failed) > 0 {
		res.Status = "failed"
		res.Message = fmt.Sprintf("killed %d, %d failed; %s",
			out.KilledCount(), len(out.Failed), res.Message)
		res.Details["failed"] = out.Failed
		emit(res)
		return 1 // controlled failure
	}
	if len(un.Errors) > 0 {
		res.Status = "failed"
		emit(res)
		return 1
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
