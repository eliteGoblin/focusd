// Package runner executes a job plugin binary and records the outcome.
//
// Contract (spec §Plugin execution contract):
//
//	platform writes resolved job config -> temp JSON file
//	exec: <binary> run --config <tmp.json>
//	stdout = structured JSON result, stderr = diagnostics
//	exit 0 = success, 1 = controlled failure, 2+ = runtime error
//
// The runner owns timeout (kill on expiry), retry, exit-code mapping,
// and persistence to job_runs (including last success/failure history).
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/eliteGoblin/focusd/platform/internal/core/plugin"
	"github.com/eliteGoblin/focusd/platform/internal/core/state"
)

// Outcome is the terminal result of a (possibly retried) job execution.
type Outcome struct {
	Status     string // state.RunStatus*
	ExitCode   int
	Message    string
	Stdout     string
	Stderr     string
	Err        string
	TimedOut   bool
	Attempts   int
	DurationMS int64
	Result     plugin.Result
}

// Runner executes plugins and persists run history.
type Runner struct {
	DB *state.DB
}

// New builds a Runner.
func New(db *state.DB) *Runner { return &Runner{DB: db} }

// Job is the minimal job spec the runner needs (decoupled from config).
type Job struct {
	ID      string
	Timeout time.Duration
	Retry   int
	Config  map[string]any
}

// Run executes the plugin for job, applying timeout and retry, and
// records every attempt in job_runs. It returns the terminal outcome.
// triggeredBy is recorded (e.g. "scheduler", "manual").
func (r *Runner) Run(ctx context.Context, job Job, p plugin.Discovered, triggeredBy string) (Outcome, error) {
	if !p.OK || p.BinaryPath == "" {
		return Outcome{}, fmt.Errorf("plugin %s is not runnable", p.Dir)
	}

	attempts := job.Retry + 1
	var last Outcome
	for attempt := 1; attempt <= attempts; attempt++ {
		out, err := r.runOnce(ctx, job, p, triggeredBy)
		if err != nil {
			return out, err
		}
		out.Attempts = attempt
		last = out
		// Terminal: success or a controlled failure (exit 1 is a real
		// job answer, not a transient error). Only error/timeout retry.
		if out.Status == state.RunStatusOK || out.Status == state.RunStatusFailed {
			break
		}
		if ctx.Err() != nil {
			break // parent cancelled; stop retrying
		}
	}
	return last, nil
}

func (r *Runner) runOnce(ctx context.Context, job Job, p plugin.Discovered, triggeredBy string) (Outcome, error) {
	cfgPath, cleanup, err := writeJobConfig(job, p.Manifest.ID)
	if err != nil {
		return Outcome{}, err
	}
	defer cleanup()

	runID, err := r.DB.Runs.Start(job.ID, p.Manifest.ID, p.Manifest.Version, triggeredBy)
	if err != nil {
		return Outcome{}, err
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if job.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, job.Timeout)
		defer cancel()
	}

	start := time.Now()
	cmd := exec.CommandContext(runCtx, p.BinaryPath, "run", "--config", cfgPath)
	configureProc(cmd)
	// Backstop: if the killed plugin (or a grandchild) still holds the
	// output pipes, force Run to return shortly after the kill instead
	// of hanging until the child exits on its own.
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr boundedBuffer
	stdout.limit, stderr.limit = 1<<20, 1<<20 // 1 MiB cap each
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	dur := time.Since(start)

	out := Outcome{
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMS: dur.Milliseconds(),
	}
	classify(&out, runCtx, runErr)

	// Best-effort parse of the structured result (only meaningful when
	// the plugin actually produced output).
	if res, perr := plugin.ParseResult([]byte(out.Stdout)); perr == nil {
		out.Result = res
		if out.Message == "" {
			out.Message = res.Message
		}
		if out.Status == state.RunStatusOK && res.Status != "" && res.Status != "ok" {
			// Exit 0 but body says not-ok: trust exit code, note mismatch.
			out.Message = fmt.Sprintf("%s (result.status=%q)", out.Message, res.Status)
		}
	} else if out.Status == state.RunStatusOK {
		// Exit 0 with unparseable JSON is a protocol violation.
		out.Status = state.RunStatusError
		out.Err = fmt.Sprintf("exit 0 but invalid result JSON: %v", perr)
	}

	stdoutJSON := out.Stdout
	if !json.Valid([]byte(stdoutJSON)) {
		stdoutJSON = "" // keep stdout_json column valid-or-empty
	}
	finishErr := r.DB.Runs.Finish(state.JobRun{
		ID: runID, DurationMS: out.DurationMS, Status: out.Status,
		ExitCode: out.ExitCode, Message: out.Message, StdoutJSON: stdoutJSON,
		StderrText: out.Stderr, ErrorText: out.Err, TimedOut: out.TimedOut,
	})
	if finishErr != nil {
		return out, finishErr
	}
	return out, nil
}

// classify maps process result + context state to a run status.
func classify(out *Outcome, ctx context.Context, runErr error) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		out.Status = state.RunStatusTimedOut
		out.TimedOut = true
		out.ExitCode = -1
		out.Err = "killed: timeout exceeded"
		return
	}
	if runErr == nil {
		out.Status = state.RunStatusOK
		out.ExitCode = 0
		return
	}
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		code := ee.ExitCode()
		out.ExitCode = code
		switch {
		case code == 1:
			out.Status = state.RunStatusFailed // controlled failure
		default:
			out.Status = state.RunStatusError // 2+ runtime error
		}
		return
	}
	// Spawn failure (binary missing, not executable, ctx cancelled, ...).
	out.Status = state.RunStatusError
	out.ExitCode = -1
	out.Err = runErr.Error()
}

func writeJobConfig(job Job, pluginID string) (string, func(), error) {
	in := plugin.JobInput{JobID: job.ID, PluginID: pluginID, Config: job.Config}
	data, err := json.Marshal(in)
	if err != nil {
		return "", func() {}, fmt.Errorf("marshal job config: %w", err)
	}
	f, err := os.CreateTemp("", "focusd-job-"+job.ID+"-*.json")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp job config: %w", err)
	}
	path := f.Name()
	cleanup := func() { os.Remove(path) }
	if _, err := f.Write(data); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("write job config: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

// boundedBuffer captures process output up to limit bytes, then drops
// the rest. A runaway plugin must not OOM the platform.
type boundedBuffer struct {
	buf      []byte
	limit    int
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 { // unbounded
		b.buf = append(b.buf, p...)
		return len(p), nil
	}
	if room := b.limit - len(b.buf); room > 0 {
		if len(p) > room {
			b.buf = append(b.buf, p[:room]...)
			b.overflow = true
		} else {
			b.buf = append(b.buf, p...)
		}
	} else {
		b.overflow = true
	}
	return len(p), nil // always report full write; we intentionally drop
}

func (b *boundedBuffer) String() string {
	if b.overflow {
		return string(b.buf) + "\n...[truncated]"
	}
	return string(b.buf)
}
