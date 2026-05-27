// Package e2e is the authoritative end-to-end test: a REAL core.Executor
// + REAL platformsvc child processes + REAL Ed25519-signed mock platform
// binaries + the Local (fake GitHub) release feed. Deterministic and
// CI-runnable. It proves the daemon's core responsibility:
//
//	resolve latest → download → verify signature → run platform
//	bad new version → crash-loop detected → marked bad → roll back to good
//
// Signing key: $FOCUSD_ED25519_PRIVATE_KEY (CI secret) or
// ~/.creds/focusd_ed25519_private.pem. Skipped if neither is present.
package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
	"github.com/eliteGoblin/focusd/daemon/internal/fetch"
	"github.com/eliteGoblin/focusd/daemon/internal/platformsvc"
	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

func moduleRoot(t *testing.T) string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(f), "..", ".."))
}

func signingKey(t *testing.T) []byte {
	if env := os.Getenv("FOCUSD_ED25519_PRIVATE_KEY"); env != "" {
		return []byte(env)
	}
	b, err := os.ReadFile(os.ExpandEnv("$HOME/.creds/focusd_ed25519_private.pem"))
	if err != nil {
		t.Skip("no signing key (env or ~/.creds); skipping e2e")
	}
	return b
}

// buildSignedMock compiles the mock platform at `version` (crashing if
// bad) and writes a SIGNED binary to relDir/<version>/platform.
func buildSignedMock(t *testing.T, root, relDir, version string, bad bool) {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "mock-"+version)
	ld := "-X main.version=" + version
	if bad {
		ld += " -X main.crashOnStart=1"
	}
	cmd := exec.Command("go", "build", "-ldflags", ld, "-o", tmp, "./cmd/mockplatform")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mock %s: %v\n%s", version, err, out)
	}
	dst := filepath.Join(relDir, version, "platform")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sig.SignFile(tmp, dst, signingKey(t)); err != nil {
		t.Fatalf("sign mock %s: %v", version, err)
	}
}

func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func TestDaemonE2E_DownloadRunRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e is not run in -short")
	}
	root := moduleRoot(t)
	rel := t.TempDir()
	wd := t.TempDir()

	const good, bad = "v1.0.0", "v1.0.1"
	buildSignedMock(t, root, rel, good, false)
	if err := os.WriteFile(filepath.Join(rel, "latest"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}

	st := &core.Store{Dir: wd}
	// Bug 3 contract change: the reconcile loop no longer auto-resolves
	// "Latest" — `daemon install -v vX.Y.Z` (production) writes the
	// desired version up front, the reconcile loop just acts on it. This
	// e2e test mirrors that by pre-seeding the desired version before
	// driving ticks. (Without this, the executor returns Blocked every
	// tick and nothing downloads — see go-reviewer CRITICAL #1.)
	if err := st.WriteDesired(good); err != nil {
		t.Fatalf("pre-seed desired: %v", err)
	}
	ps := platformsvc.New(wd)
	ps.Healthy = 400 * time.Millisecond
	ps.Unhealthy = 300 * time.Millisecond
	ex := core.NewExecutor(st, &fetch.Local{Dir: rel}, ps, nil)

	tick := func() core.Action {
		a, err := ex.Tick(context.Background())
		if err != nil {
			t.Fatalf("tick error: %v", err)
		}
		return a
	}
	drive := func(n int, gap time.Duration) {
		for i := 0; i < n; i++ {
			tick()
			time.Sleep(gap)
		}
	}

	// --- Phase 1: good version is resolved, verified, run, promoted ---
	drive(8, 150*time.Millisecond)
	if got := st.Desired(); got != good {
		t.Fatalf("desired = %q, want %q", got, good)
	}
	if !st.HaveBin(good) {
		t.Fatal("good binary was not downloaded+placed")
	}
	waitFor(t, "platform_running marker = good", 3*time.Second, func() bool {
		b, _ := os.ReadFile(filepath.Join(wd, "platform_running"))
		return string(b) == good
	})
	waitFor(t, "good promoted", 3*time.Second, func() bool {
		drive(1, 0)
		return st.Good() == good
	})
	t.Logf("PHASE1 ok: desired=%s good=%s marker=%s", st.Desired(), st.Good(), good)

	// --- Phase 2: publish a BAD latest; re-resolve; must roll back ---
	buildSignedMock(t, root, rel, bad, true)
	if err := os.WriteFile(filepath.Join(rel, "latest"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	// Bug 3 contract: `daemon update vX.Y.Z` now writes the desired
	// version directly instead of wiping it to trigger an auto-resolve.
	// Mirror that here — write the bad version as desired and let the
	// reconcile loop drive the swap.
	if err := st.WriteDesired(bad); err != nil {
		t.Fatalf("set desired=bad: %v", err)
	}
	_ = ps.Stop()

	var lastSteadyGood bool
	waitFor(t, "bad marked + rolled back to good", 12*time.Second, func() bool {
		a := tick()
		time.Sleep(200 * time.Millisecond)
		lastSteadyGood = a.Kind == core.Steady && a.Target == good
		return st.BadSet()[bad] && lastSteadyGood
	})
	if st.Good() != good {
		t.Fatalf("good must remain %q, got %q", good, st.Good())
	}
	if v, _ := ps.RunningVersion(); v != good {
		t.Fatalf("platform must be rolled back to %q, running %q", good, v)
	}
	t.Logf("PHASE2 ok: bad=%v good=%s running=%s", st.BadSet(), st.Good(), good)
}
