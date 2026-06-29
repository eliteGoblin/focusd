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
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/eliteGoblin/focusd/platform/internal/core/plugin"
	"github.com/eliteGoblin/focusd/platform/internal/core/snapshot"
	"github.com/eliteGoblin/focusd/platform/internal/core/state"
	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
)

// discardLogger is a no-op slog.Logger used when no logger is injected, so
// existing callers/tests that build a Runner without WithLogger are
// unaffected (no nil checks at the call sites).
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// integrityVerifier reconciles a single plugin's on-disk binaries against
// the genuine embedded copy, restoring any that don't match. It is the
// point-of-use integrity seam (ADR-0019): the runner calls it as
// platform/root immediately before exec — before any setuid credential is
// applied to the child — so a swapped/substitute binary is restored and
// the genuine one runs instead. Backed in production by a bundle impl;
// tests inject a fake to exercise restored / error paths without a real
// embedded bundle.
//
// VerifyOrRestore(pluginRoot, subdir) reports restored=true if it had to
// rewrite any file, the want/got sha prefixes of the first mismatched file
// (for the tamper event — never a path), and a non-nil error if the check
// itself failed (disk unreadable, etc.) — in which case the runner must
// NOT exec.
type integrityVerifier interface {
	VerifyOrRestore(pluginRoot, subdir string) (restored bool, wantPrefix, gotPrefix string, err error)
}

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
	// terminal marks an outcome that must NOT be retried within the current
	// tick even though its Status (e.g. RunStatusError) would normally be
	// retryable. Set by the point-of-use integrity-verify-error path so a
	// transient FS error records exactly one error run + one event and defers
	// to the NEXT scheduled tick — never spamming in-tick retries (ADR-0019).
	terminal bool
}

// Runner executes plugins and persists run history.
type Runner struct {
	DB *state.DB
	// Mode is the platform's run mode. It gates runtime privilege-drop:
	// only a system (root) platform steps down to the console user for a
	// run_as=current_user plugin. Defaults to ModeUser (no drop) so tests
	// and non-root platforms behave as before.
	Mode osadapter.RunMode
	// consoleUser discovers the logged-in console user (uid/gid/name/home)
	// for the privilege-drop. nil => realConsoleUser. Tests inject a fake
	// to exercise the no-console-user skip and env-reseed without root.
	consoleUser consoleUserFn
	// verifier reconciles the on-disk plugin binary against the genuine
	// embedded copy at point-of-use (ADR-0019). nil => integrity check
	// skipped (the plugin runs as-is). Production wires a bundle-backed
	// impl in app.BuildScheduler; tests inject a fake or leave it nil.
	verifier integrityVerifier
	// log is the structured action log (FEATURE 16): the runner emits
	// WARN/ERROR lines for integrity tamper / verify failures (the per-run
	// INFO "job finished" line is emitted by the scheduler, not here), so the
	// app log is an independent whitebox audit/e2e channel. nil => a discard
	// logger (set in New/NewWithMode) so existing callers and tests are
	// unaffected and call sites never nil-check.
	log *slog.Logger
	// snap mirrors each finished run into the status snapshot (the read fast
	// path that decouples `platform status` from the contended live DB). A nil
	// *snapshot.Store is a no-op, so an injected-less runner (tests, in-memory
	// DBs with no workdir) behaves exactly as before.
	snap *snapshot.Store
}

// WithVerifier returns r with the point-of-use integrity verifier set.
// Used by the composition root to inject the bundle-backed impl. Returns
// the same *Runner for fluent wiring.
func (r *Runner) WithVerifier(v integrityVerifier) *Runner {
	r.verifier = v
	return r
}

// WithLogger returns r with the structured action logger set (FEATURE 16).
// A nil logger is ignored (the discard default stays in place). Used by the
// composition root to inject the platform's app logger. Returns the same
// *Runner for fluent wiring.
func (r *Runner) WithLogger(log *slog.Logger) *Runner {
	if log != nil {
		r.log = log
	}
	return r
}

// WithSnapshot returns r wired to mirror each recorded run into the status
// snapshot (the DB-free read fast path). A nil store leaves the runner as a
// no-op writer. Returns the same *Runner for fluent wiring.
func (r *Runner) WithSnapshot(s *snapshot.Store) *Runner {
	r.snap = s
	return r
}

// recordSnapshot mirrors one terminal run into the status snapshot. It is
// best-effort: the DB row is already the source of truth, so a snapshot write
// failure is logged and swallowed rather than failing the run. nil-safe via
// the Store's nil receiver.
func (r *Runner) recordSnapshot(jobID, status string, startedAt time.Time) {
	if err := r.snap.Record(jobID, status, startedAt); err != nil {
		r.log.Warn("status snapshot write failed", "job", jobID, "err_type", fmt.Sprintf("%T", err))
	}
}

// New builds a Runner in user mode (no privilege-drop). System-mode
// platforms use NewWithMode so the runner can step down for current_user
// plugins.
func New(db *state.DB) *Runner {
	return &Runner{DB: db, Mode: osadapter.ModeUser, log: discardLogger()}
}

// NewWithMode builds a Runner for the given platform run mode. A system
// (root) runner will fork→setuid to the console user for current_user
// plugins (see privdrop.go).
func NewWithMode(db *state.DB, mode osadapter.RunMode) *Runner {
	return &Runner{DB: db, Mode: mode, log: discardLogger()}
}

// resolveConsoleUser returns the discovery seam, defaulting to the real
// `stat -f %u /dev/console` implementation when none was injected.
func (r *Runner) resolveConsoleUser() consoleUserFn {
	if r.consoleUser != nil {
		return r.consoleUser
	}
	return realConsoleUser
}

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
	// Production discovery always attaches a non-nil Manifest to an OK
	// plugin, but Run is a public API any caller (incl. tests) can reach
	// with a hand-built Discovered. runOnce dereferences p.Manifest.RunAs
	// first thing — guard it so a nil manifest is a clean error, not a
	// panic. (go + security review, LOW.)
	if p.Manifest == nil {
		return Outcome{}, fmt.Errorf("plugin %s has no manifest", p.Dir)
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
		// Terminal: success, a controlled failure (exit 1 is a real job
		// answer), unavailable (no console user — retrying inside this tick
		// won't help; the next scheduled tick retries), or an outcome the
		// callee explicitly marked terminal (integrity-verify error — one
		// error run + one event per tick, retry NEXT tick). Only
		// error/timeout otherwise retry.
		if out.terminal ||
			out.Status == state.RunStatusOK ||
			out.Status == state.RunStatusFailed ||
			out.Status == state.RunStatusUnavailable {
			break
		}
		if ctx.Err() != nil {
			break // parent cancelled; stop retrying
		}
	}
	return last, nil
}

func (r *Runner) runOnce(ctx context.Context, job Job, p plugin.Discovered, triggeredBy string) (Outcome, error) {
	// Resolve the privilege-drop plan BEFORE doing any work. If a root
	// platform must run a current_user plugin but no console user is
	// logged in, skip the tick cleanly (retry next schedule) rather than
	// writing the user's files as root → /var/root corruption.
	plan, err := resolvePlan(r.Mode, p.Manifest.RunAs, r.resolveConsoleUser())
	if err != nil {
		return Outcome{}, err
	}
	if plan.action == dropSkipNoConsoleUser {
		const reason = "no console user logged in; deferring current_user plugin"
		if rerr := r.DB.Runs.RecordUnavailable(job.ID, p.Manifest.ID, reason); rerr != nil {
			return Outcome{}, rerr
		}
		r.recordSnapshot(job.ID, state.RunStatusUnavailable, time.Now())
		return Outcome{Status: state.RunStatusUnavailable, Message: reason}, nil
	}

	// When dropping to the console user, the plugin binary + the path to
	// it must be reachable by that user (the workdir is root-owned 0700).
	if plan.action == dropToUser {
		if err := prepareDropPaths(p.BinaryPath); err != nil {
			return Outcome{}, err
		}
	}

	// Point-of-use integrity check (ADR-0019). MUST run here: after the
	// drop paths are prepared but BEFORE exec, while the runner still holds
	// the platform's own (root, in system mode) credentials — the setuid
	// drop is applied to the CHILD via configureProc, not to us, so the
	// verify+restore writes the genuine binary as root and a dropped child
	// cannot have neutered it in between. A swapped binary is restored and
	// the genuine one runs; an errored check means we do NOT exec a
	// possibly-tampered binary (record an error run + event, retry next
	// tick).
	if r.verifier != nil {
		// p.Dir is the plugin's own directory (<pluginRoot>/<subdir>),
		// always built via filepath.Join by discovery (no trailing slash).
		// Clean defensively so Base can't degrade to "." and verify the
		// whole bundle instead of this one plugin.
		dir := filepath.Clean(p.Dir)
		pluginRoot := filepath.Dir(dir)
		subdir := filepath.Base(dir)
		restored, wantPrefix, gotPrefix, verr := r.verifier.VerifyOrRestore(pluginRoot, subdir)
		if verr != nil {
			const reason = "plugin integrity check failed; refusing to run possibly-tampered binary"
			// Whitebox audit line (FEATURE 16): redaction-safe — plugin id +
			// error CLASS only. verr may embed a disguised path; log its type,
			// never its raw string, so the app log can't leak the workdir.
			r.log.Error("plugin integrity check failed",
				"plugin", p.Manifest.ID, "err_type", fmt.Sprintf("%T", verr))
			if rerr := r.DB.Runs.RecordError(job.ID, p.Manifest.ID, reason); rerr != nil {
				return Outcome{}, rerr
			}
			if rerr := r.DB.Events.RecordIntegrityCheckFailed(job.ID, p.Manifest.ID, "integrity verify errored"); rerr != nil {
				return Outcome{}, rerr
			}
			r.recordSnapshot(job.ID, state.RunStatusError, time.Now())
			// terminal=true: a verify error already recorded exactly one error
			// run + one event for this tick. Mark it terminal so Run does NOT
			// loop and re-record on a job with Retry>0 — the retry is the NEXT
			// scheduled tick, not an in-tick re-check of a transient FS fault.
			return Outcome{Status: state.RunStatusError, Message: reason, Err: verr.Error(), terminal: true}, nil
		}
		if restored {
			// Tamper detected and repaired: record the security event so
			// status can never read this job as a plain "ok" again, then
			// run the GENUINE binary that VerifyOrRestore just put back. The
			// want/got sha prefixes (never a path) make the event diagnostic.
			// Whitebox audit line (FEATURE 16): redaction-safe — plugin id +
			// sha PREFIXES only, never a path/label.
			r.log.Warn("plugin tamper repaired",
				"plugin", p.Manifest.ID, "want_sha", wantPrefix, "got_sha", gotPrefix)
			if rerr := r.DB.Events.RecordTamperRepaired(job.ID, p.Manifest.ID, wantPrefix, gotPrefix); rerr != nil {
				return Outcome{}, rerr
			}
		}
	}

	cfgPath, cleanup, err := writeJobConfig(job, p.Manifest.ID, plan)
	if err != nil {
		return Outcome{}, err
	}
	defer cleanup()

	startedAt := time.Now().UTC()
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
	configureProc(cmd, plan)
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
	// Mirror the just-finished run into the status snapshot (the DB-free read
	// fast path) using the SAME start time the DB row carries, so the snapshot
	// and DB agree on recency.
	r.recordSnapshot(job.ID, out.Status, startedAt)
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

func writeJobConfig(job Job, pluginID string, plan dropPlan) (string, func(), error) {
	in := plugin.JobInput{JobID: job.ID, PluginID: pluginID, Config: job.Config}
	data, err := json.Marshal(in)
	if err != nil {
		return "", func() {}, fmt.Errorf("marshal job config: %w", err)
	}
	// When dropping to the console user, the default temp dir is root's
	// TMPDIR (/var/folders/.../-Tmp-, mode 0700 root) which the dropped
	// uid cannot traverse to read the config. Write to /tmp (world-
	// traversable, sticky) and chown the file to the target uid so only
	// that user can read it. The config is non-sensitive job input.
	tmpDir := ""
	if plan.action == dropToUser {
		tmpDir = "/tmp"
	}
	f, err := os.CreateTemp(tmpDir, "focusd-job-"+job.ID+"-*.json")
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
	if plan.action == dropToUser {
		if err := os.Chown(path, plan.uid, plan.gid); err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("chown job config to dropped uid: %w", err)
		}
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
