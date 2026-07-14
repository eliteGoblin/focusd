// Package platformsvc implements core.Platform: it runs the platform
// binary as a child process and reports liveness/version. The daemon
// observes the OS (the process it started), never trusts a self-report.
package platformsvc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ProcSvc manages a single platform child process.
type ProcSvc struct {
	Workdir   string        // platform-workdir (passed to the child via env, HF4)
	Unhealthy time.Duration // exit sooner than this ⇒ "crashed quickly"
	Healthy   time.Duration // alive longer than this ⇒ "healthy for"
	// Argv0 is the HF4 (FEATURE 24) disguised argv[0] for the platform child.
	// When set, the child is launched with a GENERIC token as argv[0] (no path,
	// no 'platform', no version) and its workdir moves OFF the command line — the
	// child self-derives it from its own binary location — so `ps aux`/`ps -E`
	// over the live process finds nothing tied to the install. Empty ⇒ the legacy
	// argv (binPath + --workdir <wd>), keeping dev runs and the existing
	// platformsvc tests unchanged.
	Argv0 string
	// PidFile, when set, is the absolute path of the platform child's liveness
	// pidfile (HF4 FEATURE 24, P3) — a fixed-basename, SALT-INDEPENDENT file in the
	// daemon-home holding only the child's OS pid as a bare int. Start writes it
	// after launch; the exit waiter removes it. A separate `focusd status` process
	// reads it and probes the pid directly, so status is correct even if the
	// disguise salt diverged from the running child's argv. Empty ⇒ no pidfile
	// (dev runs / unit tests), preserving the legacy pgrep-only status path.
	PidFile string

	mu        sync.Mutex
	cmd       *exec.Cmd
	version   string
	startedAt time.Time
	exited    bool
	exitedAt  time.Time
	exitCh    chan struct{} // closed by the SINGLE waiter when cmd exits
}

// WorkdirEnvKey carries the platform-workdir to the child in its environment
// instead of on argv (HF4). MUST match platform osadapter.WorkdirEnvKey (the
// platform reads it in parseCommon). Duplicated literal across the module
// boundary — like MeshEnvKey — because daemon and platform are separate modules.
// The key is deliberately opaque and names neither focusd nor 'workdir'.
const WorkdirEnvKey = "APP_STATE_DIR"

// MeshEnvKey mirrors osadapter.MeshEnvKey — the launchd EnvironmentVariables key
// that carries THIS daemon's mesh role marker ("run:a" etc.). The daemon
// inherits it from launchd, so if we passed os.Environ() straight to the
// disguised platform child, the child would re-expose it in `ps -E`, tying the
// process to the mesh. The disguised branch scrubs it (along with WorkdirEnvKey).
// Duplicated literal to avoid importing osadapter here — matching the
// WorkdirEnvKey precedent; TestPlatformStartCommandHasZeroLeaks pins the effect.
const MeshEnvKey = "APP_LAUNCH_CONTEXT"

// PlatformLogName is the engine log file under the workdir. The engine's
// stdout+stderr (its slog stream, plugin job output, errors/warnings) are
// captured here so the engine is OBSERVABLE. Previously the child's stdio
// was left nil → connected to /dev/null, which silently discarded every
// engine/plugin log line and hid real failures (the silent-failure trap the
// observability principle forbids).
//
// HF4 (FEATURE 24): the basename is neutral ("svc.log", not "platform.log") so a
// filesystem grep for 'platform' does not hit the log file. MUST match the
// platform's own logging.go basename (both write this file in the workdir).
const PlatformLogName = "svc.log"

// New builds a ProcSvc with sane default windows.
func New(workdir string) *ProcSvc {
	return &ProcSvc{Workdir: workdir, Unhealthy: 3 * time.Second, Healthy: 5 * time.Second}
}

// RunningVersion returns the version of the live child, or "".
func (p *ProcSvc) RunningVersion() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil || p.exited {
		return "", nil
	}
	return p.version, nil
}

// childArgvEnv renders the platform child's DISPLAY argv and environment (HF4).
// Pure and side-effect-free so the greppability guard can assert directly.
//
//   - disguised (Argv0 set): argv = [<token>, "run"] — no path, no 'platform',
//     no version. The workdir is on NEITHER argv nor env: it is SCRUBBED from the
//     inherited environment (WorkdirEnvKey) so `ps -E` cannot show it, and the
//     child self-derives it from its own binary location. The inherited mesh role
//     marker (MeshEnvKey) is scrubbed too, so the child does not re-expose it.
//   - legacy (Argv0 empty): argv = [binPath, "--workdir", <workdir>] and env nil
//     (inherit) — byte-for-byte the pre-HF4 behavior (dev runs, tests, e2e).
func (p *ProcSvc) childArgvEnv(binPath string) (args, env []string) {
	if p.Argv0 != "" {
		return []string{p.Argv0, "run"}, scrubEnv(os.Environ(), WorkdirEnvKey, MeshEnvKey)
	}
	return []string{binPath, "--workdir", p.Workdir}, nil
}

// scrubEnv returns a copy of env with every "KEY=..." entry whose key is in keys
// removed. Used to strip the workdir (WorkdirEnvKey) and the inherited mesh role
// marker (MeshEnvKey) from the disguised platform child's environment so neither
// surfaces in `ps -E`. Returns a fresh slice (never aliases os.Environ()).
func scrubEnv(env []string, keys ...string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		drop := false
		for _, k := range keys {
			if strings.HasPrefix(kv, k+"=") {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// RunningPID returns the OS pid of the live platform child, or 0 when none is
// running. FEATURE 25: the reconcile-loop winner exempts THIS pid when it reaps
// foreign platform processes, so it can never kill its own survivor.
func (p *ProcSvc) RunningPID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil || p.exited || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Start launches binPath as the platform for version v.
func (p *ProcSvc) Start(binPath, v string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// HF4 (FEATURE 24): render the child's DISPLAY argv + env (pure helper, so the
	// greppability guard can assert no leak without launching). exec.Command sets
	// cmd.Path = binPath (the real, root-visible executable); we then override
	// cmd.Args so the LIVE process shows only the disguised argv.
	args, env := p.childArgvEnv(binPath)
	c := exec.Command(binPath)
	c.Args = args
	if env != nil {
		c.Env = env
	}
	// FEATURE 21 (HF1) self-heal: the platform-workdir is disposable and may
	// have just been wiped. Recreate it before launch so the engine has a
	// writable --workdir (state.db) and platform.log has a parent dir.
	// Best-effort: a mkdir failure degrades to the existing behavior below.
	_ = os.MkdirAll(p.Workdir, 0o700)
	// Capture the engine's stdout+stderr to <workdir>/platform.log so the
	// engine + plugins are observable. Best-effort: a log-open failure must
	// NOT block protection from starting, so we degrade to the prior
	// (discarded) behavior rather than refuse to run. The common path — the
	// workdir is writable (it already holds state.db) — always succeeds.
	logf, lerr := os.OpenFile(filepath.Join(p.Workdir, PlatformLogName),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if lerr != nil {
		// Observability must not fail SILENTLY. Record why on the daemon's
		// own stderr (captured to daemon.log) before degrading to discarded
		// engine output — so a missing platform.log is itself explained.
		fmt.Fprintf(os.Stderr, "platformsvc: cannot open %s (engine output will be discarded): %v\n", PlatformLogName, lerr)
	} else {
		c.Stdout = logf
		c.Stderr = logf
	}
	if err := c.Start(); err != nil {
		if logf != nil {
			logf.Close()
		}
		return err
	}
	exitCh := make(chan struct{})
	p.cmd = c
	p.version = v
	p.startedAt = time.Now()
	p.exited = false
	p.exitedAt = time.Time{}
	p.exitCh = exitCh

	// P3 (HF4): publish the child's pid to the salt-independent liveness pidfile
	// (under p.mu, so a concurrent Start's write and this one are serialized).
	// A separate `focusd status` reads it and probes the pid directly, so status
	// is correct even if the disguise salt diverged from the running child's argv.
	// Best-effort: a write failure must not block protection — status degrades to
	// the pgrep fallback.
	if p.PidFile != "" {
		_ = writePidFile(p.PidFile, c.Process.Pid)
	}

	// The ONLY waiter for this child. Whoever needs to know it exited
	// observes exitCh — no second Wait() (double-reap race).
	go func() {
		_ = c.Wait()
		p.mu.Lock()
		current := p.cmd == c // still the current process?
		if current {
			p.exited = true
			p.exitedAt = time.Now()
		}
		p.mu.Unlock()
		if logf != nil {
			logf.Close() // release the engine-log fd when the child exits
		}
		// Clear the pidfile only if WE are still the current child. A newer Start
		// runs under p.mu and rewrites the pidfile with its own pid before
		// releasing the lock, so a stale waiter must never clobber that entry.
		if current && p.PidFile != "" {
			_ = removePidIfMatches(p.PidFile, c.Process.Pid)
		}
		close(exitCh)
	}()
	return nil
}

// writePidFile atomically writes pid (a bare int, 0600) to path via a PID-unique
// temp + rename, so a concurrent reader never observes a half-written value.
func writePidFile(path string, pid int) error {
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// removePidIfMatches removes path only if it still names pid, so a stale exit
// waiter can never delete a newer child's pidfile.
func removePidIfMatches(path string, pid int) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	got, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return err
	}
	if got == pid {
		return os.Remove(path)
	}
	return nil
}

// Stop SIGTERMs the child and waits (via the single waiter's exitCh)
// up to 2s, then SIGKILLs. "exited" is set only by the waiter when the
// process is truly observed to have exited.
func (p *ProcSvc) Stop() error {
	p.mu.Lock()
	c, exitCh := p.cmd, p.exitCh
	p.mu.Unlock()
	if c == nil || c.Process == nil {
		return nil
	}
	_ = c.Process.Signal(syscall.SIGTERM)
	select {
	case <-exitCh:
		return nil
	case <-time.After(2 * time.Second):
		_ = c.Process.Kill()
		<-exitCh // the waiter will observe the kill and record exit
		return nil
	}
}

// CrashedQuickly reports whether version v's process exited within the
// unhealthy window (crash-loop signal).
func (p *ProcSvc) CrashedQuickly(v string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited && p.version == v &&
		p.exitedAt.Sub(p.startedAt) < p.Unhealthy
}

// HealthyFor reports whether v has stayed up beyond the healthy window.
func (p *ProcSvc) HealthyFor(v string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.exited && p.cmd != nil && p.version == v &&
		time.Since(p.startedAt) > p.Healthy
}

// ClearExit forgets a DEAD child's exit record so a stale fast-exit is not
// re-counted as a crash by the daemon's crash-loop detector. It is a no-op
// while a child is live (exited == false) — it can never disturb a running
// platform. The daemon calls this from its tamper-recovery path so reverting
// an in-place fake binary does not leave the just-replaced version wrongly
// suspected of crash-looping (the wedge stays recoverable without a daemon
// process restart). A subsequent Start re-establishes liveness/version truth.
func (p *ProcSvc) ClearExit() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.exited {
		return
	}
	p.cmd = nil
	p.version = ""
	p.exited = false
	p.exitedAt = time.Time{}
	p.startedAt = time.Time{}
}
