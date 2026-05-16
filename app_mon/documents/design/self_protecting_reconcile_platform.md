# Design: self-protecting, reconcile-based platform (focusd)

Status: agreed design. Implementation in progress (reconcile spine landed;
OS-binding layers staged behind seams). This consolidates the full design
discussion. Supersedes the heavier upgrade text in
`requirements/support_plugin_platform_refactor/enforcement_mode_server_managed.md`.

---

## 1. Purpose & threat model

focusd is a personal commitment device: when focusing, the user adds
restrictions that must be **hard to remove later, in a weak moment**.

Adversary = **the user themselves, with root / the app's own privilege**,
during low willpower. Not a remote attacker.

Consequence (accepted, stated plainly): a determined user with root can
always win eventually (wipe the install path, Recovery Mode, reinstall).
**The goal is to maximize the gap between *impulse* and *circumvention*** —
make the fast path fail in seconds and get worse, while the deliberate
path is slow enough that the urge passes. Not impossibility.

Explicitly accepted now: **if the user knows the path and wipes
everything, the app stops working. That is fine for this stage.** A
future dedicated server (deployed elsewhere, not on this Mac) will hold
backup/state so a wipe can be detected and restored. No server yet;
**local-only is the current target and is acceptable.**

## 2. Core principle: there is no inside door handle

Two distinct bypasses:

| Attack | Defense |
|---|---|
| **Kill it** (process / login item) | watchdog pair + launchd + ensurer + durable applied state |
| **Ask it to stop** (config, flag, stop cmd) | **there is no such input** |

**Remove the disable authority from the machine.** The daemon is
*tighten-only* locally: local config/policy may only ADD restrictions.
Nothing local relaxes policy. Relaxation only ever comes from a future
off-box server, signed, and time-delayed (a Ulysses contract). Until that
server exists, restrictions added are simply not locally removable except
by wiping the install (accepted, see §1).

A global "disable for upgrade" bit is **rejected**: it is exactly an
inside handle and is fragile (stuck-on after a crash = silently
unprotected). The safe shape of that idea is the **lease** (§5).

## 3. The spine: reconcile

Everything is one idempotent control loop, run by each worker every tick
(~5–10s):

```
loop:
    desired  = signed cached policy        (intent: restrictions + desired_version)
    observed = look at the world           (blocks applied? partner alive?
                                            launchd entries ok? my version?)
    decide(desired, observed) -> actions
    do(actions)                            (idempotent)
    sleep(tick)
```

One invariant governs the whole system:

> **Always: ≥1 healthy worker of the highest *valid* version, partnered,
> enforcing the signed policy.**

Anti-kill, enforcement, and upgrade are all just "drive toward the
invariant" — not separate subsystems. `Decide` is a **pure function**
(fully unit-tested); side effects are isolated in an Actuator.

## 4. Process topology

```
launchd ── plist A (KeepAlive)    → launcher → worker A ⇄ watch ⇄ worker B
launchd ── plist B (KeepAlive)    → launcher → worker B
launchd ── plist E (StartInterval ~5m) → launcher → `ensure` mode
```

- **2 symmetric workers** (identical binary, two instances), each
  reconciles policy AND watches the partner. No master/guardian
  asymmetry. The old 3-mutually-monitoring design is **not worth it** —
  launchd is a more reliable third guardian than a hand-written process.
- **launchd restarts the *process*** (KeepAlive; ~10s throttle floor).
- **The partner restores the *launchd entry*** if it was removed (a dead
  process can't fix its own autostart; the living partner does). Each
  worker repairs **both** entries (its own + partner's) — monitoring both
  is simpler than "only the other" (self-repair is a harmless no-op when
  fine).
- **Ensurer**: same binary, `ensure` mode, a 3rd launchd job on
  `StartInterval` (~5 min). It recovers the total-wipe-of-entries case
  that a running worker cannot (nothing alive to self-heal). Workers
  recreate the ensurer plist; the ensurer recreates the worker plists →
  mutual, finite (3 plists, 1 binary, 3 modes: `worker`×2 + `ensure`).

Recovery timing: killed worker is back in **≤10s** (launchd throttle
floor) — effectively ~5s because the live partner restarts it without
waiting for the throttle. During the gap, durable blocks (/etc/hosts, pf)
stay applied; only fresh actions pause (matches existing 10s quick-kill).

## 5. Versions, upgrade, lease

- Version is **baked into the binary** at build (`-ldflags -X
  main.version=vN`). A worker *is* a version; it never guesses.
- Binaries stored per version: `bin/<ver>/worker`. Keep current + prev
  (2) for rollback.
- launchd plists point at a **stable launcher path** (never rewritten).
  The launcher reads state and execs `bin/<good>/worker`. Upgrade =
  change one value (`good_version`) / repoint one `current` symlink; all
  3 modes follow on next launch. (This is why the ensurer auto-picks the
  new version — not magic, the launcher indirection.)
- `desired_version` is a field in the signed policy. Upgrade = reconcile:
  a worker on the wrong version **exits itself**; the launcher brings it
  back on the right binary. A worker never kills the partner for a
  version change (only for anti-kill, when the partner is dead).
- **Lease** = one auto-expiring SQLite row (reuse the `job_locks`
  pattern). It is **not** an off switch and never relaxes policy. It only
  answers *which of the two workers may switch version right now*, so
  upgrades are **staggered** — one canary upgrades while the other stays
  on known-good and keeps enforcing. First-come, not turns; auto-expiry
  forgives a crash mid-upgrade (self-healing, no stuck state).
- **Rollback (KISS, not a subsystem):** keep the old binary; if the new
  one dies <20s the launcher runs the previous binary and writes a
  `bad-<ver>` marker; reconcile then skips that version. Promotion:
  canary sets `good_version = desired` only after the new version stayed
  up ~30s and reconciled once. **Downgrade refused** (desired < good is a
  rollback-to-killable attack → alert, ignore).

### Upgrade timeline (normal)

```
A=v1 B=v1 good=v1 ; policy→desired=v2
A tick: drift, takes lease (canary) → A exits → launcher runs v2 ; B stays v1 enforcing
A(v2) up 30s + reconciled → good=v2, release lease
B tick: my v1 ≠ good v2 → B exits → launcher runs v2
end: A=v2 B=v2 good=v2   (never both down; never 0 enforcing)
```

### Upgrade timeline (bad v2)

```
A canary → v2 crashes <20s → launcher runs v1 + writes bad-v2
reconcile skips v2 ; B never moved (lease taken) → both on v1, still protected
```

## 6. Resilience: never let non-core break core

Tiered, fail-soft. One failing step logs + reports and the loop
continues:

```
Tier 0 (must ALWAYS run; nothing may block it):
        re-assert cached policy + keep partner & launchd entries alive
Tier 1 (best effort): pull new policy / desired_version (future server)
Tier 2 (best effort): report problems / telemetry
```

A Tier 1/2 failure is logged + queued for report, **never** stops Tier 0.
Reporting is fire-and-forget. Idempotent + atomic writes mean a crash
mid-action cannot corrupt; the next tick repairs it. Server unreachable ⇒
keep enforcing cached policy (fail **closed**).

## 7. What is implemented now vs deferred

**Implemented (this branch, tested, no OS side effects, no root):**

- `platform/internal/core/reconcile`: the pure `Decide` function and the
  `Engine` (loop + tiered execution) with seams: `PolicySource`,
  `Observer`, `LeaseStore`, `Actuator`. Lease backed by the existing
  SQLite `job_locks` pattern. Full table-driven tests of every scenario
  in §5 (steady / canary / safety-net / catch-up / refuse-downgrade /
  skip-bad / partner-dead / launch-repair / combined) and a deterministic
  end-to-end upgrade-timeline test.
- This is the spine. It structurally has **no disable input** (§2).

**Deferred (needs real OS / root / network — documented, behind seams):**

- Real launchd plist install/KeepAlive/StartInterval (the 3 plists).
- Real launcher binary + versioned `bin/<ver>` layout + symlink swap.
- Real partner process spawn + mutual launchd-entry repair.
- Real signed-binary download from GitHub + signature/anti-downgrade
  verify.
- Off-box server: signed policy, delayed release tokens, backup/restore,
  liveness. (Server is a dedicated API deployed elsewhere — future.)

These are intentionally seams so the tested spine drives them later
without rework.

## 8. Roadmap

1. ✅ Platform Phases 0–6 (orchestrator, plugins kill-steam +
   browser-monitor).
2. ✅ Reconcile spine (this doc, pure + tested).
3. Launcher/worker split + versioned layout + 3 launchd plists.
4. Partner spawn + mutual launchd-entry repair + ensurer mode.
5. Secure self-update (signed, anti-downgrade, staggered, bad-marker).
6. Off-box server: signed policy + delayed release tokens + backup.

## 9. Honest ceiling

Root + deliberate effort (wipe path, Recovery Mode) defeats this. The
design does not claim otherwise. It makes the impulsive path fail in
seconds and escalate, and the deliberate path slow and visible — which,
for a self-discipline tool, is the entire point.
