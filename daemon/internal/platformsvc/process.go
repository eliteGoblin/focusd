// Package platformsvc implements core.Platform: it runs the platform
// binary as a child process and reports liveness/version. The daemon
// observes the OS (the process it started), never trusts a self-report.
package platformsvc

import (
	"os/exec"
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
}

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
	if err := c.Start(); err != nil {
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
