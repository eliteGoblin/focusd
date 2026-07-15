package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

// Seams — real implementations hit GitHub / launchd / processes; tests
// inject fakes so the whole executor is verified without network/root.
type (
	// Fetcher resolves the latest release and downloads + installs a
	// version's verified binary into the store (Download must
	// Ed25519-verify before placing it; returns error if not genuine).
	Fetcher interface {
		ResolveLatest(ctx context.Context) (string, error)
		EnsureBinary(ctx context.Context, st *Store, version string) error
	}
	// Platform controls the platform process.
	Platform interface {
		// RunningVersion returns the version of the running platform,
		// or "" if none is running.
		RunningVersion() (string, error)
		// Start launches the platform binary at binPath for version v.
		Start(binPath, version string) error
		// Stop terminates the running platform.
		Stop() error
		// CrashedQuickly reports whether the version started last exited
		// within the unhealthy window (used for crash-loop detection).
		CrashedQuickly(version string) bool
		// HealthyFor reports whether version has stayed up long enough
		// to be promoted to "good".
		HealthyFor(version string) bool
	}
)

// Executor runs one reconcile tick: observe → core.Decide → act.
type Executor struct {
	Store *Store
	Fetch Fetcher
	Plat  Platform
	Lock  ProcessLock
	Log   *slog.Logger
	// VerifyBin verifies that the on-disk platform binary at path is a
	// genuine, Ed25519-signed release. nil ⇒ the daemon's real trust root
	// (sig.VerifyFile) — secure by default. Injected as a seam only so unit
	// tests, whose fake fetcher writes UNSIGNED stand-in binaries, can supply
	// a permissive or content-aware stub; production never overrides it. This
	// is the daemon→platform anti-tamper check: the on-disk binary is
	// re-verified before EVERY start, never trusted for mere existence.
	VerifyBin func(path string) (bool, error)
	crashHit  map[string]int // in-memory consecutive fast-exits per version
	// lastTarget is the version this executor last drove the platform
	// to (EnsureRunning/Rollback target). Crash detection keys off this
	// so a version that crashes instantly is still caught.
	lastTarget string
	// holdsLock records that this executor already won the singleton lock.
	// The lock is acquired ONCE (its fd stays open for the executor's
	// lifetime) so later ticks skip re-acquisition.
	holdsLock bool
	// fetchRetryAfter throttles platform-binary fetch retries (ADR-0015):
	// after a failed EnsureBinary the next attempt is deferred until this
	// time, so a persistently-failing fetch (network down, CDN hiccup) is
	// retried ~once per fetchRetryCooldown rather than every ~2s reconcile
	// tick. This throttles only the *fetch*, never the mesh heal cadence.
	fetchRetryAfter time.Time
	// fetchRetryVersion scopes the cooldown to the version whose fetch
	// failed. If the desired target changes (operator pins a different
	// version) the new version's first fetch must NOT be deferred by the
	// prior version's cooldown — so we only defer when v matches.
	fetchRetryVersion string
	// now is the clock seam (defaults to time.Now); tests inject a fake.
	now func() time.Time
	// lastStartAt is when this executor last (re)started the platform child.
	// The proactive workdir-wipe heal (GAP 1) suppresses its integrity check
	// for platformSettleWindow after a (re)start so the brief window before a
	// freshly-started platform has written state.db is not misread as a wipe.
	// Zero value (never started) leaves the check un-suppressed.
	lastStartAt time.Time
	// Fallback is the baked, compiled-in platform version adopted ONLY when
	// the on-disk store has no desired version (FEATURE 17, recovery
	// resilience). A wiped workdir (store gone) would otherwise leave Decide
	// permanently Blocked — no desired ⇒ no platform ⇒ no protection. With a
	// Fallback set, the first tick on an empty store re-pins it (recreating the
	// wiped workdir via WriteDesired's MkdirAll) and self-heals.
	//
	// FLOOR-not-ceiling: Fallback is consulted ONLY when the store desired is
	// empty. An explicit `install -v` / `update` WriteDesired always wins and
	// rolls forward — the fallback can never pin DOWN a newer pinned version.
	// Empty Fallback ⇒ today's safe Blocked behavior (no self-heal).
	Fallback string
	// LockFilePath is the path of the cross-generation singleton lock the
	// winning daemon flocks to elect ONE platform supervisor (FEATURE 17,
	// Item 2). It is a FIXED, mode-keyed path that survives workdir rotation
	// — NOT the per-workdir Store.LockPath() (which lets two path-rotating
	// generations each run their own platform). Empty ⇒ fall back to
	// Store.LockPath() (preserves existing tests + the test-mode per-workdir
	// isolation, which build() sets explicitly).
	LockFilePath string
	// fallbackWarned latches the one-shot "adopting baked fallback" WARN. When
	// the workdir is persistently unwritable (the desired write keeps failing)
	// the fallback branch runs every ~2s tick; without this latch it would spam
	// the log forever. We log the FIRST adoption, suppress repeats, and re-arm
	// (reset to false) the moment a real desired version is present on disk
	// again — so a later recurrence is logged afresh.
	fallbackWarned bool
	// ReapForeign, when set, SIGTERM→SIGKILLs any FOREIGN platform process (an
	// orphan that reparented to launchd after a daemon death) EXCEPT the passed
	// survivor PID. FEATURE 25: the daemon flock only ELECTS one platform, it
	// never REAPS the extras a crash/self-update cycle leaves behind. Only the
	// lock WINNER reaps (a loser yields and never fights over the process table),
	// throttled to once per reapEveryTicks. Injected as a seam because the reaper
	// is darwin/launchd-specific (osadapter) while core stays cross-platform and
	// import-cycle-free. nil ⇒ no reap (tests, non-darwin, test-mode, non-mesh).
	ReapForeign func(keepPID int) (int, error)
	// reapTick counts ticks for the reap throttle (see reapEveryTicks).
	reapTick int
}

// New builds an Executor.
func NewExecutor(st *Store, f Fetcher, p Platform, lk ProcessLock, log *slog.Logger) *Executor {
	return &Executor{Store: st, Fetch: f, Plat: p, Lock: lk, Log: log, crashHit: map[string]int{}, now: time.Now}
}

const crashThreshold = 3 // consecutive fast exits ⇒ mark version bad

// fetchRetryCooldown caps how often a failing platform-binary fetch is
// retried (ADR-0015 defense-in-depth). The reconcile loop ticks ~2s; a
// failing fetch must NOT be re-attempted every tick. This throttles the
// fetch retry only — it does NOT change the mesh worker-heal cadence.
const fetchRetryCooldown = 30 * time.Second

// platformSettleWindow is how long after WE (re)start the platform the
// proactive workdir-integrity check (GAP 1) is suppressed. A freshly-started
// platform writes state.db within milliseconds, but until it does the workdir
// briefly looks "wiped"; suppressing the check for this window avoids a
// restart loop on a healthy just-started platform. It does NOT delay the FIRST
// heal — a wipe hits an ESTABLISHED platform (started long ago), so the window
// is already past.
//
// It is deliberately >= the default crash "unhealthy" window
// (platformsvc.ProcSvc.Unhealthy, 3s): a heal-triggered Stop is always followed
// by a same-tick Start, so the exited state never survives to the next tick's
// crash check — but keeping the settle window at least as long as the unhealthy
// window means a genuinely-failing restart is caught by crash-loop detection
// (MarkBad), not by an endless re-heal. A real platform that starts but cannot
// write state.db exits (platform refuses a partial start) → RunningVersion=="" →
// the `running != ""` heal guard yields to crash detection, so heal churn can
// never itself MarkBad the only version.
const platformSettleWindow = 5 * time.Second

// HoldsPlatformLock reports whether this executor won the cross-generation
// platform singleton lock (FEATURE 17, Item 2). The reconcile loop uses it to
// gate single-writer work to exactly one mesh worker — e.g. the in-mesh binary
// re-materialize (FEATURE 22 follow-up), so roles A and B don't both place a
// fresh binary when the file is deleted. Read-only accessor over holdsLock.
func (e *Executor) HoldsPlatformLock() bool { return e.holdsLock }

// nowOrDefault returns the executor clock (time.Now unless a test injected
// a fake), tolerating zero-valued executors built without NewExecutor.
func (e *Executor) nowOrDefault() time.Time {
	if e.now != nil {
		return e.now()
	}
	return time.Now()
}

// workdirWiped reports whether the shared workdir is gone/broken AND we are
// past the post-start settle window — so a just-(re)started platform that has
// not yet written state.db is not misdetected as a wipe (which would loop the
// restart). See platformSettleWindow.
func (e *Executor) workdirWiped() bool {
	if !e.lastStartAt.IsZero() && e.nowOrDefault().Sub(e.lastStartAt) < platformSettleWindow {
		return false
	}
	return !e.Store.WorkdirIntact()
}

// Tick performs exactly one reconcile step. Returns the Action taken.
func (e *Executor) Tick(ctx context.Context) (Action, error) {
	running, err := e.Plat.RunningVersion()
	if err != nil {
		return Action{}, fmt.Errorf("observe running: %w", err)
	}

	// Defense-in-depth for the crash-loop wedge: if a version we drove to the
	// crash threshold is no longer marked bad on disk — an operator (or the
	// tamper-recovery path) cleared it — mirror that clear into memory. Reset
	// the in-memory crash counter AND the ProcSvc exit latch so the daemon
	// does not immediately re-mark it bad; otherwise clearing the on-disk bad
	// set alone would not recover without a daemon PROCESS restart. Only
	// versions past the threshold (i.e. ones we actually marked bad) are
	// eligible, so normal crash accumulation toward the threshold — and this
	// tick's own MarkBad below — are never disturbed. (State.Bad is re-read
	// fresh after crash detection so Decide sees this tick's MarkBad.)
	preBad := e.Store.BadSet()
	for v, hits := range e.crashHit {
		if hits >= crashThreshold && !preBad[v] {
			delete(e.crashHit, v)
			e.resetPlatExit()
		}
	}

	// Crash-loop detection. Check the version we last drove (lastTarget)
	// — NOT the currently-running version — because a version that
	// crashes immediately is no longer "running" yet must still be
	// detected, marked bad, and rolled back.
	cv := e.lastTarget
	if cv == "" {
		cv = running
	}
	if cv != "" {
		switch {
		case e.Plat.CrashedQuickly(cv):
			e.crashHit[cv]++
			if e.crashHit[cv] >= crashThreshold {
				_ = e.Store.MarkBad(cv)
				e.logf("version %s crash-looped (%d) → marked bad", cv, e.crashHit[cv])
			}
		case e.Plat.HealthyFor(cv):
			e.crashHit[cv] = 0
		}
	}

	// GAP 1 (v0.18.0 live): proactive workdir-wipe heal. A platform whose
	// shared workdir was wiped (rm -rf) keeps running off the now-unlinked
	// inode — RunningVersion() still reports it alive, so Decide returns Steady
	// and the wipe goes UNHEALED and UNLOGGED until the platform eventually
	// crashes on its own (~minutes, blind). Detect the broken workdir here and
	// force a restart+rebuild THIS tick: Stop the limping platform so the
	// EnsureRunning path below recreates the workdir (WriteDesired's MkdirAll +
	// the baked Fallback) and the fresh platform re-initialises state.db. Only
	// the daemon that owns the live platform child acts — a standby observes
	// running=="" (its ProcSvc has no child) — so no two daemons can fight over
	// the restart.
	if running != "" && e.workdirWiped() {
		e.logf("workdir wiped/broken while platform %s claims running → restart+rebuild", running)
		// Stop the limping platform BEFORE starting a fresh one. If the stop
		// fails we must NOT proceed to Start a second platform on the same
		// workdir — two platforms would corrupt state.db and double-apply
		// enforcement. Surface the error and retry next tick (matching apply's
		// step-3 convention, which also treats a failed Stop as fatal).
		if serr := e.Plat.Stop(); serr != nil {
			return Action{}, fmt.Errorf("stop wiped-workdir platform %s: %w", running, serr)
		}
		running = ""
	}

	desired := e.Store.Desired()
	haveConfig := e.Store.HaveConfig()

	// A real desired version is present again → re-arm the one-shot fallback
	// WARN so a future wipe is reported afresh (and not silently suppressed by
	// a latch left set from an earlier recovery).
	if desired != "" {
		e.fallbackWarned = false
	}

	// FEATURE 17 (recovery resilience): a wiped workdir leaves no desired
	// version on disk, which Decide treats as Blocked → no platform → no
	// protection. If a baked Fallback is set, adopt it: re-pin it to disk
	// (WriteDesired's MkdirAll recreates the wiped workdir) so the very next
	// tick drives EnsureRunning instead of Blocked. FLOOR-not-ceiling — this
	// runs ONLY when the store desired is empty; an explicit pin always wins.
	if desired == "" && e.Fallback != "" {
		if e.Log != nil && !e.fallbackWarned {
			e.Log.Warn("no desired version on disk; adopting baked fallback")
			e.fallbackWarned = true
		}
		// Best-effort persist. Even if the write fails (e.g. store not yet
		// writable), drive toward the fallback THIS tick so a transient FS
		// hiccup doesn't leave protection Blocked; the next tick re-attempts.
		_ = e.Store.WriteDesired(e.Fallback)
		desired = e.Fallback
		haveConfig = true
	}

	st := State{
		HaveConfig: haveConfig,
		Desired:    desired,
		Running:    running,
		Good:       e.Store.Good(),
		Bad:        e.Store.BadSet(),
	}

	// Promote: a healthy running version that equals desired becomes good.
	if running != "" && running == st.Desired && st.Good != running &&
		e.Plat.HealthyFor(running) {
		_ = e.Store.WriteGood(running)
		st.Good = running
	}

	act := Decide(st)
	if act.Kind == EnsureRunning || act.Kind == Rollback {
		e.lastTarget = act.Target
	}
	applyErr := e.apply(ctx, act)
	// FEATURE 25: after acting, the lock WINNER continuously reaps orphaned
	// platform processes so the "elect one, never reap the rest" hole can't let
	// extras accrete across crash/self-update cycles.
	e.maybeReapForeign()
	return act, applyErr
}

// reapEveryTicks throttles the continuous foreign-platform reap so the winner
// scans the process table roughly once per this many reconcile ticks rather than
// every tick. At the ~2s worker cadence this is ~10s.
const reapEveryTicks = 5

// maybeReapForeign reaps orphaned platform processes when this executor is the
// lock WINNER and has a live platform to exempt. Structurally incapable of
// reaching zero platforms: it only runs when a survivor PID is known, always
// exempts it, and the daemon keeps + restarts that survivor. A standby (lock
// loser) never reaps, so two daemons never fight over the process table.
func (e *Executor) maybeReapForeign() {
	if e.ReapForeign == nil || !e.holdsLock {
		return
	}
	e.reapTick++
	// Reap on the first winning tick, then every reapEveryTicks after — prompt
	// on startup, throttled thereafter.
	if (e.reapTick-1)%reapEveryTicks != 0 {
		return
	}
	pl, ok := e.Plat.(interface{ RunningPID() int })
	if !ok {
		return // platform impl can't report a PID → cannot exempt → do not reap
	}
	keepPID := pl.RunningPID()
	if keepPID <= 0 {
		return // no live survivor to exempt → never risk reaping the last one
	}
	if n, err := e.ReapForeign(keepPID); err != nil {
		e.logf("reap foreign platforms failed (best-effort)")
	} else if n > 0 {
		e.logf("reaped %d foreign platform process(es)", n)
	}
}

func (e *Executor) apply(ctx context.Context, a Action) error {
	switch a.Kind {
	case EnsureRunning, Rollback:
		v := a.Target

		// Step 0 — singleton gate. Single-mesh runs daemon roles A and B as
		// independent processes; each would otherwise start its OWN platform
		// child on the shared workdir. A crash-safe, fd-tied OS advisory lock
		// (held by the DAEMON, not the platform) elects exactly one. The
		// winner supervises the single platform; the loser yields quietly and
		// starts NOTHING — so there is no phantom child exit for the uptime-
		// based crash detector to misread as a crash (no false rollback).
		// Acquired ONCE before any Stop/Start so a standby never tears down
		// the holder's child; the fd stays open for the executor's lifetime.
		if !e.holdsLock {
			// FEATURE 17 (Item 2): elect ONE platform across path-rotating
			// generations via a FIXED, mode-keyed lock path (LockFilePath),
			// not the per-workdir Store.LockPath() — a rotated workdir would
			// otherwise give each generation its own lock and run two
			// platforms. Empty LockFilePath ⇒ per-workdir path (test-mode
			// isolation + existing tests).
			lockPath := e.LockFilePath
			if lockPath == "" {
				lockPath = e.Store.LockPath()
			}
			ok, err := e.Lock.TryAcquire(lockPath)
			if err != nil {
				return fmt.Errorf("singleton lock: %w", err)
			}
			if !ok {
				return nil // peer owns the platform; yield (NOT a crash, NOT Blocked)
			}
			e.holdsLock = true
		}

		// Step 1 — ensure the new binary is on disk, PRESENT AND Ed25519-
		// verified, BEFORE we touch the running platform. A bare existence
		// check (HaveBin ⇒ os.Stat) is not enough: an in-place-tampered
		// platform binary (an attacker with workdir write swaps the bytes but
		// keeps the path) would otherwise be exec'd unverified. So we re-verify
		// the on-disk binary's signature on EVERY start and treat a MISSING
		// binary OR a verify FAILURE identically — drop it and re-fetch (which
		// downloads + Ed25519-verifies the genuine release before placing it).
		// This is the daemon→platform mirror of the plugin runner's
		// point-of-use VerifyOrRestore one layer up: verify-before-exec, so a
		// fake platform binary is reverted to the genuine one and NEVER runs.
		// If the fetch fails (network outage, a bad release on GitHub) we
		// return the error WITHOUT having stopped anything — the old platform
		// keeps running uninterrupted. Replacement-running invariant first.
		if !e.Store.HaveBin(v) || !e.binGenuine(v) {
			// ADR-0015 fetch-retry cooldown: a fetch that failed recently is
			// not re-attempted until fetchRetryAfter, so a persistent failure
			// (network down, CDN hiccup) is retried ~once/30s instead of every
			// ~2s tick. The old platform keeps running meanwhile.
			if now := e.nowOrDefault(); v == e.fetchRetryVersion && now.Before(e.fetchRetryAfter) {
				return fmt.Errorf("ensure binary %s: deferred until %s (fetch cooldown)", v, e.fetchRetryAfter.Format(time.RFC3339))
			}
			if err := e.Fetch.EnsureBinary(ctx, e.Store, v); err != nil {
				e.fetchRetryAfter = e.nowOrDefault().Add(fetchRetryCooldown)
				e.fetchRetryVersion = v
				return fmt.Errorf("ensure binary %s: %w", v, err)
			}
			e.fetchRetryAfter = time.Time{} // success: clear the cooldown
			e.fetchRetryVersion = ""
			// A genuine, signature-verified binary for v is now on disk (freshly
			// fetched, or reverted from an in-place tamper). Wipe any stale
			// "bad"/crash verdict about v — it was about the reverted bytes, not
			// this binary — so a wedge needs no daemon process restart.
			e.clearTamperSuspicion(v)
		}

		// Step 2 — snapshot the current running version BEFORE stopping
		// it, so a failed start can roll back.
		prevRunning, _ := e.Plat.RunningVersion()

		// Step 3 — only now stop the old, if it's a different version.
		if prevRunning != "" && prevRunning != v {
			if err := e.Plat.Stop(); err != nil {
				return fmt.Errorf("stop %s: %w", prevRunning, err)
			}
		}

		// Step 3.5 — final anti-tamper re-check IMMEDIATELY before exec.
		// Step 1's verify and this Start are separated by Stop() (up to ~2s),
		// during which a workdir-writer could swap the just-verified bytes back
		// to a fake (TOCTOU). Re-check here to shrink that window to a few
		// syscalls. On mismatch we refuse to exec the tampered bytes and return
		// — the next tick's Step 1 re-fetches the genuine binary and starts it.
		if !e.binGenuine(v) {
			return fmt.Errorf("start %s: on-disk binary failed signature re-check at exec time (tamper?)", v)
		}

		// Step 4 — start the new. If this fails AND we just stopped a
		// previously-running version, roll back to it (its binary is
		// still on disk) — but ONLY if that binary is itself signature-genuine.
		// The rollback is an exec too, so it must obey verify-before-exec: an
		// attacker could tamper the idle prevRunning binary and induce a
		// Start(v) failure to get the fake exec'd. A tampered rollback target
		// is refused (focusd is left down; the next tick re-fetches genuine).
		// Best-effort otherwise: a failed rollback is preferable to silently
		// leaving focusd stopped.
		//
		// Architect-review #3: after a successful rollback the crash
		// detector must track the actually-running version (prev), not
		// the failed-to-start target. Override lastTarget here so the
		// next tick's CrashedQuickly check keys off the right version
		// (otherwise a crashing prev would never be detected because
		// the detector would still be watching the dead target).
		if err := e.Plat.Start(e.Store.BinPath(v), v); err != nil {
			switch {
			case prevRunning != "" && prevRunning != v && e.Store.HaveBin(prevRunning) && e.binGenuine(prevRunning):
				if rbErr := e.Plat.Start(e.Store.BinPath(prevRunning), prevRunning); rbErr == nil {
					e.lastTarget = prevRunning
					e.lastStartAt = e.nowOrDefault() // rollback (re)started the platform
					if e.Log != nil {
						e.Log.Warn("start failed; rolled back to previously-running version",
							"target", v, "rolled_back_to", prevRunning, "err", err)
					}
				} else {
					if e.Log != nil {
						e.Log.Error("start failed; rollback ALSO failed — focusd is down",
							"target", v, "rollback_target", prevRunning,
							"err", err, "rollback_err", rbErr)
					}
				}
			case prevRunning != "" && prevRunning != v && e.Store.HaveBin(prevRunning):
				// prevRunning present on disk but FAILS signature re-check —
				// refuse to exec a tampered rollback target.
				if e.Log != nil {
					e.Log.Error("start failed; refused tampered rollback target — focusd is down",
						"target", v, "rollback_target", prevRunning, "err", err)
				}
			}
			return fmt.Errorf("start %s: %w", v, err)
		}
		e.lastStartAt = e.nowOrDefault() // platform (re)started successfully
		e.logf("%s → running %s", a.Kind, v)
		return nil

	case Steady:
		return nil
	case Blocked:
		e.logf("BLOCKED: %s", a.Note)
		return nil
	}
	return nil
}

// binGenuine reports whether the on-disk platform binary for v passes
// Ed25519 signature verification against the daemon's compiled-in trust root.
// A verify ERROR (unreadable / too-short / truncated file) counts as NOT
// genuine — fail closed: a binary we cannot PROVE is authentic must be
// treated exactly like a missing one and re-fetched before exec. Uses
// e.VerifyBin (the injected seam) or, when nil, the real sig.VerifyFile.
func (e *Executor) binGenuine(v string) bool {
	verify := e.VerifyBin
	if verify == nil {
		verify = sig.VerifyFile
	}
	ok, err := verify(e.Store.BinPath(v))
	return err == nil && ok
}

// clearTamperSuspicion drops every stale crash/bad verdict about v once a
// genuine binary for it is confirmed on disk: the on-disk bad marker, the
// in-memory crash counter, and the ProcSvc exit latch. Clearing the on-disk
// bad set alone would not suffice — an in-memory crashHit + a latched
// fast-exit would re-mark v bad on the next tick — so all three are cleared
// together, keeping the wedge recoverable without a daemon process restart.
func (e *Executor) clearTamperSuspicion(v string) {
	// Never swallow a real ClearBad failure: if the on-disk marker can't be
	// removed (e.g. permission), the in-memory clear below would desync from
	// disk and Decide would keep routing v to Rollback/Blocked with no trail.
	if err := e.Store.ClearBad(v); err != nil {
		e.logf("clear bad marker for %s failed (will retry next tick): %v", v, err)
	}
	delete(e.crashHit, v)
	e.resetPlatExit()
}

// resetPlatExit forgets a dead platform child's exit record when the Platform
// implementation supports it (ProcSvc does; the test/reap fakes do not). A
// no-op while a child is live, so it can never disturb a running platform.
func (e *Executor) resetPlatExit() {
	if c, ok := e.Plat.(interface{ ClearExit() }); ok {
		c.ClearExit()
	}
}

func (e *Executor) logf(format string, args ...any) {
	if e.Log != nil {
		e.Log.Info(fmt.Sprintf(format, args...))
	}
}
