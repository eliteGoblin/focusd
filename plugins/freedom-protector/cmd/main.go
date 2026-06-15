// Command freedom-protector is a focusd job plugin that keeps the
// third-party Freedom focus app (Freedom.to) and its proxy process alive,
// and makes a best-effort, honestly-recorded attempt at keeping Freedom's
// macOS background/login item enabled. It follows the platform plugin
// contract (protocol v1):
//
//	freedom-protector run --config <path-to-job-config.json>
//
// Input  : JSON file {job_id, plugin_id, config:{app_path?, app_process?,
//
//	proxy_process?, proxy_port?, proxy_rpcport?}} — all optional;
//	empty config => grounded defaults.
//
// Output : structured JSON result on stdout, diagnostics on stderr.
// Exit   : 0 success (or benign skip) · 1 controlled failure (a relaunch
//
//	failed) · 2 plugin error (process enumeration / config error)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/eliteGoblin/focusd/plugins/freedom-protector/internal/reconciler"
)

var version = "dev"

// reconcileBudget self-limits the reconcile so the plugin flushes its JSON
// result and exits cleanly BEFORE the platform's 20s job timeout hard-kills
// it (SIGKILL would drop the result and trigger a retry storm). Kept under
// 20s with headroom for process start, config read, and stdout flush.
const reconcileBudget = 15 * time.Second

// jobInput mirrors the platform's plugin.JobInput. Duplicated here so the
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

// run is the contract entrypoint, split from main so tests can invoke it
// with controlled args and inspect the exit code.
func run(args []string) int {
	if len(args) >= 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Println("freedom-protector", version)
		return 0
	}
	if len(args) < 1 || args[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: freedom-protector run --config <path>")
		return 2
	}

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to resolved job config JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	opts, err := loadOptions(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		emit(result{Status: "error", Message: err.Error()})
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), reconcileBudget)
	defer cancel()
	out, err := reconciler.New(opts).Reconcile(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reconcile error:", err)
		emit(result{Status: "error", Message: err.Error()})
		return 2
	}

	details := map[string]any{
		"skipped":         out.Skipped,
		"scanned":         out.Scanned,
		"app_running":     out.AppRunning,
		"proxy_running":   out.ProxyRunning,
		"relaunched":      out.Relaunched,
		"login_item_note": out.LoginItemNote,
	}
	if out.SkipReason != "" {
		details["skip_reason"] = out.SkipReason
	}
	if len(out.Failed) > 0 {
		details["failed"] = out.Failed
	}

	if out.Skipped {
		emit(result{Status: "ok",
			Message: fmt.Sprintf("skipped: %s", out.SkipReason),
			Details: details})
		return 0
	}

	if len(out.Failed) > 0 {
		emit(result{Status: "failed",
			Message: fmt.Sprintf("relaunched=%v, %d launch(es) failed",
				out.Relaunched, len(out.Failed)),
			Details: details})
		return 1 // controlled failure
	}

	emit(result{Status: "ok",
		Message: fmt.Sprintf("app_running=%v proxy_running=%v relaunched=%v",
			out.AppRunning, out.ProxyRunning, out.Relaunched),
		Details: details})
	return 0
}

// loadOptions reads the optional config knobs from the job config JSON.
// "" path => all defaults.
func loadOptions(path string) (reconciler.Options, error) {
	if path == "" {
		return reconciler.Options{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return reconciler.Options{}, fmt.Errorf("read config: %w", err)
	}
	var in jobInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return reconciler.Options{}, fmt.Errorf("parse config JSON: %w", err)
	}
	return reconciler.Options{
		AppPath:      stringField(in.Config, "app_path"),
		AppProcess:   stringField(in.Config, "app_process"),
		ProxyProcess: stringField(in.Config, "proxy_process"),
		ProxyPort:    stringField(in.Config, "proxy_port"),
		ProxyRPCPort: stringField(in.Config, "proxy_rpcport"),
	}, nil
}

// stringField returns config[key] when present and a string, else "".
// Absent/typed-wrong values fall back to defaults rather than erroring —
// the knobs are optional overrides, not required input.
func stringField(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	if s, ok := cfg[key].(string); ok {
		return s
	}
	return ""
}

// emit writes the result JSON to stdout for the runner to parse. A blank
// stdout makes the runner treat exit 0 as "valid exit but invalid result
// JSON" and triggers a retry storm — so emit valid JSON even on a Marshal
// failure (mirrors skill-protector).
func emit(r result) {
	b, err := json.Marshal(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, "emit: marshal:", err)
		fmt.Println(`{"status":"error","message":"emit marshal failed"}`)
		return
	}
	fmt.Println(string(b))
}
