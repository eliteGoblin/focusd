// Package platformsvc implements core.Platform: it runs the platform
// binary as a child process and reports liveness/version. The daemon
// observes the OS (the process it started), never trusts a self-report.
package platformsvc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	// no 'platform', no version) and its --workdir moves OFF the command line
	// into WorkdirEnvKey — so `ps aux | grep` over the live process finds nothing
	// tied to the install. Empty ⇒ the legacy argv (binPath + --workdir <wd>),
	// keeping dev runs and the existing platformsvc tests unchanged.
	Argv0 string

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
//     no version; the workdir moves into WorkdirEnvKey in the returned env so
//     `ps` never shows it. Env is os.Environ()+the workdir entry.
//   - legacy (Argv0 empty): argv = [binPath, "--workdir", <workdir>] and env nil
//     (inherit) — byte-for-byte the pre-HF4 behavior (dev runs, tests, e2e).
func (p *ProcSvc) childArgvEnv(binPath string) (args, env []string) {
	if p.Argv0 != "" {
		return []string{p.Argv0, "run"}, append(os.Environ(), WorkdirEnvKey+"="+p.Workdir)
	}
	return []string{binPath, "--workdir", p.Workdir}, nil
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

	// The ONLY waiter for this child. Whoever needs to know it exited
	// observes exitCh — no second Wait() (double-reap race).
	go func() {
		_ = c.Wait()
		p.mu.Lock()
		if p.cmd == c { // still the current process
			p.exited = true
			p.exitedAt = time.Now()
		}
		p.mu.Unlock()
		if logf != nil {
			logf.Close() // release the engine-log fd when the child exits
		}
		close(exitCh)
	}()
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
