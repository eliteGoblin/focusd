//go:build darwin

package osadapter

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
)

// --- minimal core.Fetcher / core.Platform fakes for the integration ---------

type noopFetch struct{}

func (noopFetch) ResolveLatest(context.Context) (string, error)           { return "", nil }
func (noopFetch) EnsureBinary(context.Context, *core.Store, string) error { return nil }

// runningPlat reports a fixed running version + PID so the executor drives one
// EnsureRunning (acquiring the singleton lock) and then exempts the survivor PID
// in its reap tick. It never spawns anything — the REAL survivor/orphan are
// spawned separately by the test.
type runningPlat struct {
	pid     int
	version string
	started []string
}

func (p *runningPlat) RunningVersion() (string, error) { return p.version, nil }
func (p *runningPlat) RunningPID() int                 { return p.pid }
func (p *runningPlat) Start(_, v string) error {
	p.started = append(p.started, v)
	p.version = v
	return nil
}
func (p *runningPlat) Stop() error                { p.version = ""; return nil }
func (p *runningPlat) CrashedQuickly(string) bool { return false }
func (p *runningPlat) HealthyFor(string) bool     { return false }

// TestUpgradePath_ExecutorReapsOrphan_NoDuplicate (Test #5, integration): the
// FULL Element-3 chain end to end — a REAL core.Executor, the REAL
// ReapForeignPlatforms core, and REAL spawned platform processes. After an
// upgrade the old generation's platform lingers as an orphan; the new daemon's
// reconcile tick (lock winner) reaps it while exempting its own survivor, so
// EXACTLY ONE platform remains — the upgrade leaves no orphan.
func TestUpgradePath_ExecutorReapsOrphan_NoDuplicate(t *testing.T) {
	root := reapRoot(t)
	orphanPID := spawnFakePlatform(t, root, ".oldgen", "v0.16.3")   // pre-upgrade leftover
	survivorPID := spawnFakePlatform(t, root, ".newgen", "v0.16.4") // the new daemon's platform

	// A real executor: desired pinned, binary already on disk (skip fetch), a
	// real fd-tied singleton flock (wins on a fresh temp path), and the REAL
	// reaper anchored to the sandbox root.
	daemonHome := t.TempDir()
	st := &core.Store{Dir: daemonHome}
	if err := st.WriteDesired("v0.16.4"); err != nil {
		t.Fatal(err)
	}
	p := &runningPlat{pid: survivorPID} // running == "" initially → first tick EnsureRunning
	e := core.NewExecutor(st, noopFetch{}, p, core.NewFileLock(), nil)
	e.LockFilePath = filepath.Join(root, "singleton.lock")
	e.ReapForeign = func(keepPID int) (int, error) {
		return reapForeignPlatforms(root, keepPID, "", listPlatformProcs, resolvePlatformExecs, signedVerify(), killProc)
	}

	// GATE: both platforms genuinely alive before the tick.
	if !isAlive(orphanPID) || !isAlive(survivorPID) {
		t.Fatal("pre-condition: orphan + survivor must both be alive")
	}

	// First tick: EnsureRunning acquires the flock (holdsLock=true), "starts" the
	// survivor, then the winner's reap tick fires.
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	waitDead(t, orphanPID)
	if !isAlive(survivorPID) {
		t.Fatal("the new daemon's survivor platform must remain (exempt by PID)")
	}

	// Exactly one platform under this root survives the upgrade.
	if remaining := countLivePlatformsUnder(t, root, signedVerify()); remaining != 1 {
		t.Fatalf("upgrade must leave EXACTLY ONE platform, got %d", remaining)
	}
}
