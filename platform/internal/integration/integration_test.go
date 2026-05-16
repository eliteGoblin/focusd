// Package integration exercises the full contract: a real, separately
// built plugin binary driven through the real runner + state DB. This is
// the spec's "integration-style tests with fake/real plugins" at the
// highest fidelity (no root required — uses a process name that cannot
// match anything, so nothing is actually killed).
package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/platform/internal/core/plugin"
	"github.com/eliteGoblin/focusd/platform/internal/core/runner"
	"github.com/eliteGoblin/focusd/platform/internal/core/state"
)

// buildKillSteam compiles the real plugin into dir. Skips (not fails) if
// the toolchain/module cache is unavailable so offline CI stays green.
func buildKillSteam(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("kill-steam targets darwin")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "../../.."))
	pluginSrc := filepath.Join(repoRoot, "plugins", "kill-steam")
	bin := filepath.Join(dir, "kill-steam")

	cmd := exec.Command("go", "build", "-o", bin, "./cmd")
	cmd.Dir = pluginSrc
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build kill-steam plugin (offline?): %v\n%s", err, out)
	}
	return bin
}

func TestRealKillSteamPluginThroughRunner(t *testing.T) {
	tmp := t.TempDir()
	pluginDir := filepath.Join(tmp, "plugins", "kill-steam")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := buildKillSteam(t, pluginDir)

	manifest := `{"id":"kill-steam","name":"Kill Steam","version":"1.0.0",
"type":"job","protocol_version":"1","entrypoint":"./kill-steam",
"supported_os":["` + runtime.GOOS + `"],"supported_arch":["` + runtime.GOARCH + `"],
"required_privilege":"user","run_as":"current_user"}`
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := state.Open(filepath.Join(tmp, "state.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer db.Close()

	disc := &plugin.Discoverer{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Mode: "user"}
	found, err := disc.Discover(filepath.Join(tmp, "plugins"))
	if err != nil || len(found) != 1 || !found[0].OK {
		t.Fatalf("discovery failed: %v %+v", err, found)
	}
	if found[0].BinaryPath != bin {
		t.Errorf("resolved binary %q, want %q", found[0].BinaryPath, bin)
	}

	r := runner.New(db)
	job := runner.Job{
		ID:      "kill_steam_periodic",
		Timeout: 10 * time.Second,
		Config:  map[string]any{"process_names": []any{"zzz-focusd-it-nonexistent"}},
	}
	out, err := r.Run(context.Background(), job, found[0], "manual")
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}
	if out.Status != state.RunStatusOK || out.ExitCode != 0 {
		t.Fatalf("unexpected outcome: %+v", out)
	}
	if kc, ok := out.Result.Details["killed_count"]; !ok || kc.(float64) != 0 {
		t.Errorf("expected killed_count 0, got %v", out.Result.Details)
	}

	// History must be persisted with the parsed JSON result.
	last, err := db.Runs.LastByStatus("kill_steam_periodic", state.RunStatusOK)
	if err != nil || last.StdoutJSON == "" || last.PluginVersion != "1.0.0" {
		t.Errorf("run not persisted correctly: %+v err=%v", last, err)
	}
}
