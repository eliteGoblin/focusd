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
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

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

	raw, err := readJobConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		emit(result{Status: "error", Message: err.Error()})
		return 2
	}
	names, err := loadNames(raw)
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

	res, code := buildVerdict(out, un)
	emit(res)
	return code
}

// buildVerdict maps a kill + uninstall outcome to the plugin result and exit
// code. It is a pure function (no I/O) so the exit-code/status wiring — the
// externally observed contract — is unit-tested directly. Any of: failed
// kills, surviving Steam processes, an unrunnable post-kill re-scan, or
// uninstall errors is a controlled failure (status=failed, exit 1). The
// verdict must NEVER read as a clean "ok" while Steam survives OR while its
// removal could not be verified (green-over-dead-protection).
func buildVerdict(out killer.Outcome, un uninstaller.Outcome) (result, int) {
	base := fmt.Sprintf("scanned=%d killed=%d uninstall_detected=%v removed=%d",
		out.Scanned, out.KilledCount(), un.Detected, len(un.Removed))
	res := result{
		Status:  "ok",
		Message: base,
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

	var reasons []string
	if len(out.Failed) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d kill(s) failed", len(out.Failed)))
		res.Details["failed"] = out.Failed
	}
	if len(out.Survivors) > 0 {
		reasons = append(reasons, fmt.Sprintf("Steam still present after kill (survivors=%d)", len(out.Survivors)))
		res.Details["survivors"] = out.Survivors
	}
	if out.RescanError != "" {
		// Verification could not run → we cannot confirm Steam is gone. Refuse
		// to report green over an unverified pass.
		reasons = append(reasons, "post-kill verification failed: "+out.RescanError)
		res.Details["rescan_error"] = out.RescanError
	}
	if len(un.Errors) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d uninstall error(s)", len(un.Errors)))
	}

	if len(reasons) == 0 {
		return res, 0
	}
	res.Status = "failed"
	res.Message = strings.Join(reasons, "; ") + "; " + base
	return res, 1 // controlled failure
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

// loadNames reads optional config.process_names; empty/nil raw => defaults.
func loadNames(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
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
