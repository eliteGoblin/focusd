package runner

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/platform/internal/core/state"
	"github.com/eliteGoblin/focusd/platform/internal/testutil"
)

func newRunner(t *testing.T) *Runner {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

func TestRunSuccess(t *testing.T) {
	r := newRunner(t)
	p := testutil.ScriptPlugin(t, "ok-plugin",
		`echo '{"status":"ok","message":"Steam not running","details":{"killed_count":0}}'
exit 0`)
	out, err := r.Run(context.Background(), Job{ID: "j1"}, p, "scheduler")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Status != state.RunStatusOK || out.ExitCode != 0 {
		t.Fatalf("unexpected outcome: %+v", out)
	}
	if out.Result.Message != "Steam not running" {
		t.Errorf("result not parsed: %+v", out.Result)
	}
	last, err := r.DB.Runs.LastByStatus("j1", state.RunStatusOK)
	if err != nil || last.StdoutJSON == "" {
		t.Errorf("run not persisted: %v %+v", err, last)
	}
}

func TestRunControlledFailure(t *testing.T) {
	r := newRunner(t)
	p := testutil.ScriptPlugin(t, "fail-plugin",
		`echo '{"status":"failed","message":"could not kill"}'
exit 1`)
	out, _ := r.Run(context.Background(), Job{ID: "j1"}, p, "scheduler")
	if out.Status != state.RunStatusFailed || out.ExitCode != 1 {
		t.Fatalf("expected controlled failure, got %+v", out)
	}
}

func TestRunRuntimeError(t *testing.T) {
	r := newRunner(t)
	p := testutil.ScriptPlugin(t, "err-plugin", `echo "boom" >&2
exit 3`)
	out, _ := r.Run(context.Background(), Job{ID: "j1"}, p, "scheduler")
	if out.Status != state.RunStatusError || out.ExitCode != 3 {
		t.Fatalf("expected runtime error, got %+v", out)
	}
	if out.Stderr == "" {
		t.Error("stderr not captured")
	}
}

func TestRunTimeoutKills(t *testing.T) {
	r := newRunner(t)
	p := testutil.ScriptPlugin(t, "slow-plugin", `sleep 5
echo '{"status":"ok"}'`)
	start := time.Now()
	out, _ := r.Run(context.Background(), Job{ID: "j1", Timeout: 150 * time.Millisecond}, p, "scheduler")
	if !out.TimedOut || out.Status != state.RunStatusTimedOut {
		t.Fatalf("expected timeout, got %+v", out)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("process not killed promptly on timeout")
	}
	last, _ := r.DB.Runs.LastByStatus("j1", state.RunStatusTimedOut)
	if !last.TimedOut {
		t.Error("timed_out not persisted")
	}
}

func TestRunInvalidJSONOnExitZeroIsError(t *testing.T) {
	r := newRunner(t)
	p := testutil.ScriptPlugin(t, "badjson", `echo 'not json at all'
exit 0`)
	out, _ := r.Run(context.Background(), Job{ID: "j1"}, p, "scheduler")
	if out.Status != state.RunStatusError {
		t.Fatalf("exit 0 + invalid JSON must be error, got %+v", out)
	}
	if out.Err == "" {
		t.Error("expected protocol-violation error message")
	}
}

func TestRunRetryOnErrorThenSucceed(t *testing.T) {
	r := newRunner(t)
	// Fails (exit 2) until a marker file exists, then succeeds.
	marker := filepath.Join(t.TempDir(), "done")
	p := testutil.ScriptPlugin(t, "flaky",
		`if [ -f `+marker+` ]; then echo '{"status":"ok"}'; exit 0; fi
touch `+marker+`
exit 2`)
	out, err := r.Run(context.Background(), Job{ID: "j1", Retry: 2}, p, "scheduler")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Status != state.RunStatusOK {
		t.Fatalf("expected eventual success, got %+v", out)
	}
	if out.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", out.Attempts)
	}
	hist, _ := r.DB.Runs.History("j1", 10)
	if len(hist) != 2 {
		t.Errorf("expected 2 recorded runs, got %d", len(hist))
	}
}

func TestRunControlledFailureNotRetried(t *testing.T) {
	r := newRunner(t)
	p := testutil.ScriptPlugin(t, "fail1", `exit 1`)
	out, _ := r.Run(context.Background(), Job{ID: "j1", Retry: 3}, p, "scheduler")
	if out.Status != state.RunStatusFailed || out.Attempts != 1 {
		t.Errorf("exit 1 must not be retried: %+v", out)
	}
}

func TestRunNotRunnablePlugin(t *testing.T) {
	r := newRunner(t)
	p := testutil.ScriptPlugin(t, "x", `exit 0`)
	p.OK = false
	if _, err := r.Run(context.Background(), Job{ID: "j1"}, p, "scheduler"); err == nil {
		t.Error("expected error for non-runnable plugin")
	}
}

func TestRunPassesJobConfigToPlugin(t *testing.T) {
	r := newRunner(t)
	// Plugin echoes back the config it was given ON STDIN (HF4: no --config path
	// in argv; the resolved job config arrives via stdin).
	p := testutil.ScriptPlugin(t, "echocfg",
		`cat; echo '{"status":"ok"}'`)
	job := Job{ID: "j1", Config: map[string]any{"process_names": []any{"Steam"}}}
	out, err := r.Run(context.Background(), job, p, "manual")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !contains(out.Stdout, "process_names") || !contains(out.Stdout, "Steam") {
		t.Errorf("job config not delivered to plugin: %q", out.Stdout)
	}
}

func TestRunStderrHeavyAndOutputBounded(t *testing.T) {
	r := newRunner(t)
	// Emit ~2 MiB to stdout (over the 1 MiB cap) then succeed-ish.
	p := testutil.ScriptPlugin(t, "loud",
		`head -c 2097152 /dev/zero | tr '\0' 'x'
echo "diagnostic noise" >&2
exit 2`)
	out, _ := r.Run(context.Background(), Job{ID: "j1"}, p, "scheduler")
	if out.Status != state.RunStatusError {
		t.Fatalf("expected error exit, got %+v", out.Status)
	}
	if len(out.Stdout) > (1<<20)+64 {
		t.Errorf("stdout not bounded: %d bytes", len(out.Stdout))
	}
	if !contains(out.Stdout, "truncated") {
		t.Error("expected truncation marker")
	}
	if !contains(out.Stderr, "diagnostic noise") {
		t.Error("stderr not captured")
	}
}

func TestBoundedBufferNoLimit(t *testing.T) {
	var b boundedBuffer // limit 0 => unbounded-ish, no overflow marker
	b.Write([]byte("hello"))
	if b.String() != "hello" {
		t.Errorf("got %q", b.String())
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
