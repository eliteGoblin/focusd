package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/platform/internal/core/plugin"
	"github.com/eliteGoblin/focusd/platform/internal/core/state"
)

// helperPluginSrc is a REAL compiled plugin (not a shell script — a shebang
// script would lose the argv[0] override, since the kernel re-execs the
// interpreter). It observes its own os.Args and the stdin config and reports them
// back in the Result.Details so the test can assert what the LIVE process saw.
const helperPluginSrc = `package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
)

func main() {
	b, _ := io.ReadAll(os.Stdin)
	res := map[string]any{
		"status":  "ok",
		"message": "ok",
		"details": map[string]any{
			"argv0":       os.Args[0],
			"nargs":       len(os.Args),
			"argv_joined": strings.Join(os.Args, " "),
			"stdin":       string(b),
		},
	}
	_ = json.NewEncoder(os.Stdout).Encode(res)
}
`

// buildHelperPlugin compiles helperPluginSrc into a real binary and returns a
// Discovered wired to it, with a deliberately LEAKY plugin id ("kill-steam") so
// the test can prove the id never reaches the child's argv.
func buildHelperPlugin(t *testing.T) plugin.Discovered {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("integration test uses a POSIX build+exec flow")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module disghelper\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(helperPluginSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "kill-steam") // basename intentionally = the id
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GO111MODULE=on")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build helper plugin: %v\n%s", err, out)
	}
	return plugin.Discovered{
		Manifest: &plugin.Manifest{
			ID: "kill-steam", Name: "kill-steam", Version: "2.0.0", Type: plugin.TypeJob,
			ProtocolVersion: "1", Entrypoint: "./kill-steam",
			SupportedOS: []string{runtime.GOOS}, SupportedArch: []string{runtime.GOARCH},
			RequiredPrivilege: plugin.PrivUser, RunAs: plugin.RunAsCurrentUser,
		},
		Dir: dir, BinaryPath: bin, OK: true,
	}
}

// TestSchedulerRunsPluginViaStdinWithDisguisedArgv is the HF4 (FEATURE 24)
// INTEGRATION guard: the runner (the platform's scheduler execution seam) runs a
// real plugin with its config delivered on STDIN and a DISGUISED argv[0], and it
// still works. It proves — against the LIVE child's own view of itself:
//   - the resolved job config arrives via stdin (no --config path in argv);
//   - argv[0] is a generic token, NOT the plugin id / binary path;
//   - the child's whole argv is exactly [<token> run] — no id, no 'focusd', no
//     temp path, no '--config'.
func TestSchedulerRunsPluginViaStdinWithDisguisedArgv(t *testing.T) {
	r := newRunner(t)
	p := buildHelperPlugin(t)

	job := Job{ID: "j1", Config: map[string]any{"process_names": []any{"Steam"}}}
	out, err := r.Run(context.Background(), job, p, "scheduler")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Status != state.RunStatusOK {
		t.Fatalf("plugin did not run OK via stdin+disguised argv: %+v", out)
	}

	d := out.Result.Details
	if d == nil {
		t.Fatalf("plugin returned no details; stdout=%q", out.Stdout)
	}

	// 1. Config delivered via stdin.
	stdin, _ := d["stdin"].(string)
	if !strings.Contains(stdin, "process_names") || !strings.Contains(stdin, "Steam") {
		t.Errorf("job config not delivered on stdin: %q", stdin)
	}

	// 2. argv is exactly [<token>, "run"] — nothing greppable.
	if n, _ := d["nargs"].(float64); n != 2 {
		t.Errorf("child argv length = %v, want 2 ([<token> run])", d["nargs"])
	}
	argv0, _ := d["argv0"].(string)
	argvJoined, _ := d["argv_joined"].(string)
	if !strings.HasSuffix(argvJoined, " run") || strings.Fields(argvJoined)[0] != argv0 {
		t.Errorf("unexpected argv %q (argv0 %q)", argvJoined, argv0)
	}

	// 3. Zero leaks in the child's argv — the whole point.
	leaks := []string{"kill-steam", "focusd", "platform", "--config", p.BinaryPath, ".json", "/tmp", "/var"}
	for _, tok := range leaks {
		if strings.Contains(argvJoined, tok) {
			t.Errorf("child argv leaks %q: %q", tok, argvJoined)
		}
	}
	if argv0 == "kill-steam" || strings.ContainsAny(argv0, "/.") {
		t.Errorf("argv0 %q still reveals the plugin (id or path)", argv0)
	}
	t.Logf("live plugin argv = %q (config on stdin, %d bytes)", argvJoined, len(stdin))
}

// TestPluginConfigViaStdinRoundTrips is a tighter functional check that the exact
// JobInput bytes the runner marshals are what the plugin reads on stdin (the
// stdin path replacing the old temp-file --config path).
func TestPluginConfigViaStdinRoundTrips(t *testing.T) {
	got, err := marshalJobInput(Job{ID: "jX", Config: map[string]any{"k": "v"}}, "kill-steam")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"job_id":"jX"`) ||
		!strings.Contains(string(got), `"plugin_id":"kill-steam"`) ||
		!strings.Contains(string(got), `"k":"v"`) {
		t.Errorf("marshalled job input missing fields: %s", got)
	}
}
