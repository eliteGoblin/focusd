// Command skill-protector is the focusd job plugin that keeps the
// Claude-side guardrails in place. It writes three artifacts under
// $HOME/.claude/ and merges one SessionStart hook into settings.json.
//
// Contract (focusd platform plugin protocol v1):
//
//	skill-protector run --config <path-to-job-config.json>
//
// Optional flag --home overrides $HOME (tests only). The job config
// JSON is read but currently ignored — the plugin has no tuning knobs.
// Output : structured JSON result on stdout, diagnostics on stderr.
// Exit   : 0 success · 1 controlled failure (settings malformed) · 2 plugin error
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/eliteGoblin/focusd/plugins/skill-protector/internal/reconciler"
)

var version = "dev"

type result struct {
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func main() { os.Exit(run(os.Args[1:])) }

// run is the contract entrypoint. Split from main so tests can invoke
// it with controlled args and inspect the exit code.
func run(args []string) int {
	if len(args) >= 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Println("skill-protector", version)
		return 0
	}
	if len(args) < 1 || args[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: skill-protector run --config <path> [--home <dir>]")
		return 2
	}

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to resolved job config JSON")
	homeOverride := fs.String("home", "", "override $HOME (tests only)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	// Config is currently unused (no tuning knobs), but we still drain it
	// per the plugin contract so the parent's pipe write completes — the
	// disguised (stdin) path never leaves the config bytes in argv. A read
	// error is surfaced the same way sibling plugins surface config errors.
	if _, err := readJobConfig(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		emit(result{Status: "error", Message: err.Error()})
		return 2
	}

	home := *homeOverride
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "resolve $HOME:", err)
			emit(result{Status: "error", Message: err.Error()})
			return 2
		}
		home = h
	}

	r := reconciler.New(home)
	out, err := r.Reconcile()

	details := map[string]any{
		"written":         out.Written,
		"noop":            out.Noop,
		"settings_status": out.SettingsStatus,
	}
	if out.SettingsError != "" {
		details["settings_error"] = out.SettingsError
	}

	if err != nil {
		// Two cases: settings.json malformed (controlled failure, exit 1)
		// or a deeper plugin error (exit 2). The reconciler returns the
		// settings error verbatim with SettingsStatus="error".
		if out.SettingsStatus == "error" {
			emit(result{Status: "failed",
				Message: fmt.Sprintf("settings.json merge failed: %v", err),
				Details: details})
			return 1
		}
		emit(result{Status: "error", Message: err.Error(), Details: details})
		return 2
	}

	emit(result{
		Status: "ok",
		Message: fmt.Sprintf("written=%d noop=%d settings=%s",
			len(out.Written), out.Noop, out.SettingsStatus),
		Details: details,
	})
	return 0
}

// readJobConfig returns the job-config JSON bytes: from --config <path>
// (compat) when set, else drained from stdin (the disguised path — the
// config path never appears in this process's argv). Empty/absent => nil.
// skill-protector has no tuning knobs, so callers discard the bytes; the
// drain still matters so the parent's pipe write completes. HF4 (FEATURE 24).
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

// emit writes the result JSON to stdout for the runner to parse. A
// blank stdout makes the runner treat exit 0 as "exit 0 but invalid
// result JSON" and triggers an error retry storm — so we MUST emit
// valid JSON even when Marshal somehow fails. (Go-reviewer HIGH.)
func emit(r result) {
	b, err := json.Marshal(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, "emit: marshal:", err)
		fmt.Println(`{"status":"error","message":"emit marshal failed"}`)
		return
	}
	fmt.Println(string(b))
}
