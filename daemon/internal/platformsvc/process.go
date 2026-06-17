// Package platformsvc implements core.Platform: it runs the platform
// binary as a child process and reports liveness/version. The daemon
// observes the OS (the process it started), never trusts a self-report.
package platformsvc

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// ProcSvc manages a single platform child process.
type ProcSvc struct {
	Workdir   string        // passed to the platform as --workdir
	Unhealthy time.Duration // exit sooner than this ⇒ "crashed quickly"
	Healthy   time.Duration // alive longer than this ⇒ "healthy for"

	mu        sync.Mutex
	cmd       *exec.Cmd
	version   string
	startedAt time.Time
	exited    bool
	exitedAt  time.Time
	exitCh    chan struct{} // closed by the SINGLE waiter when cmd exits
	logf      *os.File      // captures the child's stdout+stderr; closed by the waiter
}

// PlatformLogName is the engine log file under the workdir. The engine's
// stdout+stderr (its slog stream, plugin job output, errors/warnings) are
// captured here so the engine is OBSERVABLE. Previously the child's stdio
// was left nil → connected to /dev/null, which silently discarded every
// engine/plugin log line and hid real failures (the silent-failure trap the
// observability principle forbids).
const PlatformLogName = "platform.log"

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

// Start launches binPath as the platform for version v.
func (p *ProcSvc) Start(binPath, v string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	c := exec.Command(binPath, "--workdir", p.Workdir)
	// Capture the engine's stdout+stderr to <workdir>/platform.log so the
	// engine + plugins are observable. Best-effort: a log-open failure must
	// NOT block protection from starting, so we degrade to the prior
	// (discarded) behavior rather than refuse to run. The common path — the
	// workdir is writable (it already holds state.db) — always succeeds.
	logf, lerr := os.OpenFile(filepath.Join(p.Workdir, PlatformLogName),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if lerr == nil {
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
	p.logf = logf

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
