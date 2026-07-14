package osadapter

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// prober is the launchd-introspection seam used by SelfUpdate's
// post-bootstrap health poll. The real implementation calls
// `launchctl print` and parses the State/PID fields; tests pass a
// scripted fake.
type prober interface {
	// isLoaded reports whether the label is registered with launchd.
	isLoaded(label string) bool
	// hasPID reports whether the label currently has a live PID
	// (i.e. launchd has spawned the worker, not just registered it).
	hasPID(label string) bool
}

// binPlacer is the binary-write seam. Real on darwin: write bytes
// atomically with mode 0o755 (Go's adhoc Mach-O signature survives
// the rename; we do NOT manually re-codesign). Tests inject a fake
// to assert place/remove ordering.
type binPlacer interface {
	place(srcBytes []byte, dstPath string) error
	remove(path string) error
}

// SelfUpdate is the path-rotating daemon swap (feature 1.5).
//
// AMFI premise: macOS caches a binary's CDHash by absolute path. After
// our previous-release daemon ran from <workdir>/<oldName>, replacing
// the bytes in place defeats launch even though the new bytes are
// fine. Self-update therefore writes the new daemon to a NEW disguised
// basename in the SAME hidden workdir, renders three new plists at
// new label filenames, and bootstraps the new mesh before tearing the
// old one down. The workdir, version.json, good marker, bin/, and any
// other state are NEVER touched.
//
// This function is the orchestration only — the caller is expected to
// have already (1) discovered cur via FindCurrentInstall and (2)
// downloaded + Ed25519-verified newBin via fetch. newSpec MUST carry:
//
//   - SelfPath = the NEW rotated binary path inside cur.Workdir
//   - Roster   = a NEW set of 3 independent disguised labels (FEATURE 10
//     / ADR-0014) so old/new plists can coexist for the swap. Generated
//     via relocate.GenerateRoster (distinct vendor families, no shared
//     base, no role token).
//   - Workdir  = cur.Workdir (we never touch the workdir on update)
//
// keepOld=true skips the final best-effort cleanup of old plists +
// the old binary file — useful for debugging the AMFI premise on a
// real machine. The OLD launchd entries are still booted out either
// way (otherwise we'd end up with 6 daemons fighting).
//
// Returns nil only when the new mesh is loaded AND health-poll
// passed AND old mesh entries are no longer registered with launchd.
// afterSwap, when non-nil, runs a BOUNDED same-mode convergence AFTER the new
// mesh is healthy and the old mesh is booted out (FEATURE 25, Element 2): reap
// the old platform child that reparented to launchd + survived our bootout, and
// sweep stale platform-workdirs. It is deliberately NOT a full both-modes
// RetireOtherGenerations — self-update's in-place rotation transiently looks
// like two generations, and retiring the "other" one here would tear down the
// install mid-swap. Injected as a seam so the post-swap hook is unit-testable.
func SelfUpdate(
	cur CurInstall, newSpec Spec, newBin []byte,
	c controller, fs fsio, p prober, b binPlacer, rs rosterIO,
	healthyTimeout, probeInterval time.Duration,
	keepOld bool,
	afterSwap func(),
) error {
	// Pre-flight: the caller's FindCurrentInstall must have found a
	// COMPLETE install. Bail loudly if not — silently bootstrapping
	// into a fresh mesh is the kind of footgun that turns a
	// self-update into an unintended install. Copilot #1: tightened
	// from binary+plists-only check to full-mesh check.
	if cur.BinaryPath == "" || len(cur.PlistPaths) != len(AllRoles) {
		return fmt.Errorf("osadapter/selfupdate: incomplete install — found %d of %d mesh plists, binary=%q",
			len(cur.PlistPaths), len(AllRoles), cur.BinaryPath)
	}
	// FEATURE 10 / ADR-0014: newSpec must carry the rotated SelfPath, the
	// preserved Workdir, AND a full 3-label Roster (the new independent
	// mesh labels). An empty/short Roster would make Spec.Label fall back
	// to the dev labels and collide the new mesh with… nothing useful —
	// reject up front. Label resolution is positional over AllRoles, so
	// the roster must have exactly len(AllRoles) non-empty entries.
	if newSpec.SelfPath == "" || newSpec.Workdir == "" {
		return errors.New("osadapter/selfupdate: newSpec missing SelfPath/Workdir")
	}
	if len(newSpec.Roster) != len(AllRoles) {
		return fmt.Errorf("osadapter/selfupdate: newSpec.Roster has %d labels, want %d",
			len(newSpec.Roster), len(AllRoles))
	}
	for i, l := range newSpec.Roster {
		if l == "" {
			return fmt.Errorf("osadapter/selfupdate: newSpec.Roster[%d] is empty", i)
		}
	}
	if newSpec.Workdir != cur.Workdir {
		// Workdir migration is OUT of scope and would strand state.db,
		// version.json, the bin/v* cache, daemon.log. Copilot #1.
		return fmt.Errorf("osadapter/selfupdate: newSpec.Workdir=%q must match current install workdir=%q (workdir is preserved across self-update)",
			newSpec.Workdir, cur.Workdir)
	}
	if newSpec.SelfPath == cur.BinaryPath {
		// The whole point of self-update is path rotation; same path
		// hits the AMFI bug we're working around.
		return errors.New("osadapter/selfupdate: new SelfPath must differ from current (AMFI requires path rotation)")
	}

	// C. Place new binary at newSpec.SelfPath.
	if err := b.place(newBin, newSpec.SelfPath); err != nil {
		return fmt.Errorf("osadapter/selfupdate: place new binary: %w", err)
	}

	// D. Render + write 3 new plists at new label filenames.
	newPlists := make([]string, 0, len(AllRoles))
	newLabels := make([]string, 0, len(AllRoles))
	for _, r := range AllRoles {
		label := newSpec.Label(r)
		pp := fs.plistPath(label)
		if err := fs.write(pp, Plist(newSpec, r)); err != nil {
			// Rollback the binary + any plists already written.
			rollback(c, fs, b, newSpec, newPlists, newLabels)
			return fmt.Errorf("osadapter/selfupdate: write new plist %s: %w", label, err)
		}
		newPlists = append(newPlists, pp)
		newLabels = append(newLabels, label)
	}

	// E. Bootstrap the new mesh in install order (A, B, ensure).
	bootedNew := make([]string, 0, len(newLabels))
	for i, label := range newLabels {
		// Bootout first (idempotent: a stale entry for the same
		// label would refuse bootstrap).
		_ = c.bootout(label)
		if err := c.bootstrap(newPlists[i]); err != nil {
			// Rollback: bootout any of the new labels we already
			// bootstrapped (reverse order), then remove plists +
			// binary. OLD install untouched.
			rollbackBootouts(c, bootedNew)
			rollback(c, fs, b, newSpec, newPlists, newLabels)
			return fmt.Errorf("osadapter/selfupdate: bootstrap new %s: %w", label, err)
		}
		bootedNew = append(bootedNew, label)
	}

	// F. Health poll: wait until all 3 new labels are loaded AND A+B
	// have PIDs, for 2 consecutive successful probes. Timeout → roll
	// back. (StartInterval ensurer may not have a live PID between
	// invocations — only A/B are checked for PID presence.)
	if err := pollHealthy(p, newLabels, healthyTimeout, probeInterval); err != nil {
		rollbackBootouts(c, bootedNew)
		rollback(c, fs, b, newSpec, newPlists, newLabels)
		return fmt.Errorf("osadapter/selfupdate: health: %w", err)
	}

	// F.5 Persist the NEW masked roster, overwriting the stale one the OLD
	// mesh left behind (FEATURE 10 / ADR-0014). Without this, the workdir
	// .roster still names the OLD labels: a new worker's cold-start read
	// would succeed (valid mask) yet recover the wrong mesh, and ensureAll
	// wouldn't rewrite it because the read didn't error.
	//
	// FEATURE 14 / ADR-0018: the new minimized plists NO LONGER carry the
	// roster in their --roster argv, so this masked file is now the SOLE
	// cold-start roster source for the new mesh. A write failure does not
	// roll back an otherwise-healthy swap (the new mesh is already serving),
	// but it leaves a stale/nil roster for the next FindCurrentInstall —
	// surface it loudly so the gap is diagnosable from the daemon log
	// instead of swallowed.
	if rs != nil {
		if err := rs.writeRoster(rosterLabels(newSpec)); err != nil {
			fmt.Fprintf(os.Stderr,
				"WARN osadapter/selfupdate: persist masked roster failed (swap kept; next cold-start roster may be stale/nil): %v\n",
				err)
		}
	}

	// G. SWAP. Bootout old in REVERSE order (ensurer first, then B,
	// then A) so the ensurer cannot respawn an old sibling between
	// bootouts. We launchctl-bootout only; we do NOT pkill -f because
	// argv overlap with the new daemon would kill them too.
	//
	// Copilot #3: errors from bootout-old are SILENTLY IGNORED here
	// (truthful description). The new mesh is healthy and serving;
	// a stuck-old bootout would require operator cleanup but does NOT
	// roll back the swap. No plumbed error reporter today — see
	// follow-up if log-noise tolerance demands one.
	for i := len(cur.Labels) - 1; i >= 0; i-- {
		_ = c.bootout(cur.Labels[i]) // see comment above re: discarded err
	}

	// G.5 FEATURE 25 (Element 2): the old daemon is booted out, but its platform
	// child reparented to launchd and SURVIVES — so reap it (bounded, same-mode)
	// before it lingers as an orphan a standby would double. Best-effort: a
	// reap/sweep failure must never fail the (already-healthy) swap.
	if afterSwap != nil {
		afterSwap()
	}

	// H. Best-effort cleanup of old plists + old binary. Errors are
	// SILENTLY IGNORED (truthful description). The new mesh is
	// serving; leftover old files are dead weight, not a correctness
	// problem. Copilot #4.
	if !keepOld {
		for _, pp := range cur.PlistPaths {
			_ = fs.remove(pp)
		}
		_ = b.remove(cur.BinaryPath)
	}

	return nil
}

// pollHealthy waits up to timeout for every label in newLabels to be
// loaded, plus the A/B workers to have a live PID, for two consecutive
// successful probes. Returns nil on success, error on timeout.
func pollHealthy(p prober, newLabels []string, timeout, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	deadline := time.Now().Add(timeout)
	consecutive := 0
	for {
		if probeOK(p, newLabels) {
			consecutive++
			if consecutive >= 2 {
				return nil
			}
		} else {
			consecutive = 0
		}
		// Copilot #2: declare timeout ONLY after the deadline passes;
		// sleep min(interval, time.Until(deadline)) so we still probe
		// once near the deadline even when interval > remaining. The
		// previous "check before sleep" form bailed early (e.g.
		// timeout=15s, interval=10s would exit at ~10s without a
		// final probe).
		now := time.Now()
		if !now.Before(deadline) {
			return fmt.Errorf("health-poll timeout after %s (last consecutive ok = %d)",
				timeout, consecutive)
		}
		remaining := deadline.Sub(now)
		sleep := interval
		if sleep > remaining {
			sleep = remaining
		}
		time.Sleep(sleep)
	}
}

func probeOK(p prober, newLabels []string) bool {
	for _, l := range newLabels {
		if !p.isLoaded(l) {
			return false
		}
	}
	// A + B must have live PIDs; ensure (StartInterval) may be idle
	// between ticks and that is fine. FEATURE 10 / ADR-0014: worker
	// detection is POSITIONAL — newLabels is built in AllRoles order, so
	// index i maps to AllRoles[i]. This must NOT key on label text: the
	// independent labels no longer carry a .a/.b/.ensure role token, and
	// a text-based guess would mis-classify the ensurer as a worker (or
	// vice-versa) and false-fail the health poll → a botched migration
	// self-update that leaves the old mesh double-running.
	for i, l := range newLabels {
		if i < len(AllRoles) && isWorkerRole(AllRoles[i]) && !p.hasPID(l) {
			return false
		}
	}
	return true
}

// isWorkerRole reports whether r is a KeepAlive worker (A or B) rather
// than the StartInterval ensurer. The health poll requires a live PID
// only for workers.
func isWorkerRole(r Role) bool { return r != RoleEnsure }

// rollbackBootouts boots out labels in reverse-order. Used when a
// failure interrupts the new-mesh bootstrap or health-poll.
func rollbackBootouts(c controller, labels []string) {
	for i := len(labels) - 1; i >= 0; i-- {
		_ = c.bootout(labels[i])
	}
}

// rollback removes new plists + new binary on failure. The OLD install
// is never touched.
func rollback(c controller, fs fsio, b binPlacer, newSpec Spec,
	newPlists, newLabels []string) {
	// Defensive: bootout any of the new labels (idempotent if not
	// loaded). rollbackBootouts is the explicit one used when we
	// know what was bootstrapped; this is the catch-all.
	for i := len(newLabels) - 1; i >= 0; i-- {
		_ = c.bootout(newLabels[i])
	}
	for _, pp := range newPlists {
		_ = fs.remove(pp)
	}
	_ = b.remove(newSpec.SelfPath)
}
