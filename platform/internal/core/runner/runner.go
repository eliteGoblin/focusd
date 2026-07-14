// Package runner executes a job plugin binary and records the outcome.
//
// Contract (spec §Plugin execution contract):
//
//	platform marshals the resolved job config -> plugin STDIN (HF4 FEATURE 24)
//	exec: <disguised-argv0> run          (argv[0] spoofed; no path/id/version)
//	stdin  = resolved job config JSON
//	stdout = structured JSON result, stderr = diagnostics
//	exit 0 = success, 1 = controlled failure, 2+ = runtime error
//
// HF4 moved the config OFF argv (it used to be `run --config <tmp.json>`, which
// leaked the temp path + plugin id to `ps`) onto stdin — an inherited pipe fd
// the setuid child reads with no widened file permissions. A `--config <path>`
// flag is retained on each plugin as a CLI-only fallback for manual invocation;
// the scheduler always uses stdin.
//
// The runner owns timeout (kill on expiry), retry, exit-code mapping,
// and persistence to job_runs (including last success/failure history).
package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	// afterPin is a TEST-ONLY seam (nil in production) fired immediately after
	// the point-of-use check pins the verified content and BEFORE the pre-exec
	// TOCTOU guard re-checks it. A test uses it to deterministically simulate a
	// binary swap landing in the verify→exec window, exercising the guard's
	// refuse path (FEATURE 23, Fix 2). Never set outside tests.
	afterPin func(binaryPath string)
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
	// verifiedSum pins the exact bytes the point-of-use check just confirmed
	// genuine, so the TOCTOU guard below can prove the binary hasn't been
	// swapped in the window between verify and exec (FEATURE 23, Fix 2). Zero
	// value unless a verifier is wired (no-verifier runs behave as before).
	var verifiedSum [32]byte
	var havePin bool
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
			return r.integrityRefuse(job, p.Manifest.ID, reason, "integrity verify errored", verr)
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
		// Capture the now-genuine content so the pre-exec guard can detect a
		// swap that lands after this point. A read failure here means we can't
		// pin the binary — treat it exactly like a verify error (do not exec).
		sum, herr := hashFile(p.BinaryPath)
		if herr != nil {
			const reason = "plugin integrity check failed; could not pin verified binary"
			return r.integrityRefuse(job, p.Manifest.ID, reason, "pin hash failed", herr)
		}
		verifiedSum, havePin = sum, true
		// Test-only: simulate a swap landing in the verify→exec window.
		if r.afterPin != nil {
			r.afterPin(p.BinaryPath)
		}
	}

	// HF4 (FEATURE 24): the resolved job config is fed to the plugin on STDIN,
	// not via `run --config <tmp.json>`. This removes the temp-file PATH (and the
	// plugin id it embedded) from the child's argv — a live `ps` shows no path,
	// no id, no 'focusd'. It also drops the old root→dropped-user chown dance:
	// stdin is an inherited pipe fd, readable by the setuid child with no file
	// permissions to widen. The plugin keeps a `--config` fallback for direct/
	// manual invocation, but the scheduler always uses stdin.
	cfgBytes, err := marshalJobInput(job, p.Manifest.ID)
	if err != nil {
		return Outcome{}, err
	}

	// TOCTOU guard (FEATURE 23, Fix 2). VerifyOrRestore confirmed the binary
	// genuine, but a handful of statements (drop-path prep, config write) run
	// before exec — a root attacker could rename-swap the path in that window
	// and run a substitute. Re-hash here and compare to the pinned genuine
	// content: a mismatch (or an unreadable file) means the binary changed
	// since verify, so refuse and defer to the NEXT tick, which re-verifies +
	// restores + runs the genuine copy. This runs BEFORE Runs.Start so a
	// refusal records exactly one error run (no dangling started row), matching
	// the verify-error path. The tradeoff is honest: Runs.Start (a SQLite write)
	// plus exec.CommandContext/configureProc/pipe setup still execute AFTER this
	// comparison, so a sub-millisecond swap landing in THAT tail is not caught —
	// a residual window, not full closure.
	//
	// A hard inode pin (exec an open fd) would close that tail, but is not
	// portable: macOS — the primary target — rejects execve(/dev/fd/N) with
	// EACCES and has no fexecve(2). The re-hash guard is the honest, portable
	// closure and matches ADR-0019's stance: fast self-heal + detection against
	// an impulsive swap, not an unbreakable seal against a scripted root racing
	// the check.
	if havePin {
		nowSum, herr := hashFile(p.BinaryPath)
		if herr != nil || nowSum != verifiedSum {
			const reason = "plugin binary changed between verify and exec; refusing to run"
			return r.integrityRefuse(job, p.Manifest.ID, reason, "binary changed post-verify", herr)
		}
	}

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
	cmd := exec.CommandContext(runCtx, p.BinaryPath, "run")
	// HF4 (FEATURE 24): override argv[0] with a generic per-exec token so the
	// live process shows e.g. `worker run` — no plugin id, no path, no version.
	// The kernel still execs p.BinaryPath (the honest, root-visible limit); only
	// the greppable argv is neutralized. The resolved config rides on stdin.
	cmd.Args[0] = osadapter.RandomPluginArgv0()
	cmd.Stdin = bytes.NewReader(cfgBytes)
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

// integrityRefuse records exactly one error run + one integrity-check-failed
// event (terminal for this tick) and returns the refusal Outcome, so the
// runner never execs a binary it could not confirm genuine (ADR-0019 / FEATURE
// 23). Shared by the verify-error, pin-hash, and TOCTOU-swap paths. cause may
// be nil (a detected swap has no underlying error). Redaction-safe: logs the
// plugin id + error CLASS only — cause may embed a disguised path, so its raw
// string never reaches the app log. terminal=true so a job with Retry>0 does
// NOT re-run the verifier in-tick; the retry is the next scheduled tick.
func (r *Runner) integrityRefuse(job Job, pluginID, runReason, eventReason string, cause error) (Outcome, error) {
	r.log.Error("plugin integrity check failed",
		"plugin", pluginID, "err_type", fmt.Sprintf("%T", cause))
	if rerr := r.DB.Runs.RecordError(job.ID, pluginID, runReason); rerr != nil {
		return Outcome{}, rerr
	}
	if rerr := r.DB.Events.RecordIntegrityCheckFailed(job.ID, pluginID, eventReason); rerr != nil {
		return Outcome{}, rerr
	}
	r.recordSnapshot(job.ID, state.RunStatusError, time.Now())
	errStr := ""
	if cause != nil {
		errStr = cause.Error()
	}
	return Outcome{Status: state.RunStatusError, Message: runReason, Err: errStr, terminal: true}, nil
}

// hashFile returns the sha256 of the file at path. Used to pin the exact bytes
// the point-of-use check confirmed genuine, so a swap between verify and exec
// is detectable (FEATURE 23, Fix 2).
func hashFile(path string) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [32]byte{}, err
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
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

// marshalJobInput renders the resolved job config as the JSON the plugin reads
// on stdin (HF4 / FEATURE 24). Replaces the old writeJobConfig temp-file+chown
// path: with stdin there is no file whose path would appear in argv and no
// permission to widen for a privilege-dropped child (stdin is an inherited pipe).
func marshalJobInput(job Job, pluginID string) ([]byte, error) {
	in := plugin.JobInput{JobID: job.ID, PluginID: pluginID, Config: job.Config}
	data, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("marshal job config: %w", err)
	}
	return data, nil
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
