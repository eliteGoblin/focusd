# ADR-0016 — Out-of-band watchdog rail: cron + same-binary second copy, local recovery

- **Status:** accepted (2026-06-16)
- **Feature:** [FEATURE 12](../features/12-out-of-band-watchdog.md)
- **Decided by:** Frank (product owner), user-locked + BA review

## Context

The in-band self-protection mesh (two mutually-respawning workers + one periodic
ensurer backstop) recovers in ≤2s from every **partial** attack — verified live,
including a 241-pass kill-storm. The one proven residual is a **total atomic
teardown**: delete all three plists + kill all processes + wipe the workdir at
once. The ensurer that would rebuild is itself an in-band mesh member, so it dies
with the mesh. After a total wipe **nothing survives to rebuild protection**.

Closing this requires a recovery agent on a rail the mesh teardown does not
touch, and that does not reintroduce the fetch-storm failure mode recorded in
ADR-0015 (so recovery must be local, not a network fetch).

## Decision

Add an **out-of-band watchdog** on a **separate rail (cron)**, implemented as a
**second copy of the same daemon binary** placed outside the mesh workdir,
invoked once a minute by a root cron entry via a new `daemon watchdog`
subcommand. It checks for a healthy mesh and, if absent, runs the **existing**
install path (fresh labels, per FEATURE 10) — **locally, no network fetch**.

- **`daemon install` becomes one-shot:** mesh + watchdog binary copy + cron entry.
- **Mutual guarding:** the mesh's reconcile also re-installs the cron entry and
  watchdog copy if removed. Two rails guard each other; keeping protection down
  requires wiping both at once, repeatedly.
- **Self-update keeps the second copy in sync:** a stale copy self-corrects on
  the next update.

## Alternatives considered

- **Network re-fetch on recovery (re-download the installer).** Rejected: it
  reintroduces the fetch-storm bug from ADR-0015 and adds a network dependency to
  the recovery path. Local recovery from an on-disk copy is simpler and safer.
- **A separate, dedicated watchdog program.** Rejected as not-KISS: a second
  codebase to build, sign, update, and keep in sync. Reusing the same daemon
  binary behind a new subcommand reuses battle-tested install code.
- **A fourth in-band launchd entry as the backstop.** Rejected: anything on the
  launchd rail dies in the same total teardown that kills the mesh — it would not
  survive the very attack it is meant to defeat. The whole point is a *different*
  rail.
- **A LaunchDaemon-with-StartInterval instead of cron.** Considered; cron was
  chosen because it is a genuinely distinct mechanism and was empirically
  confirmed to fire on this machine. Cron's fragility is recorded as a limitation
  rather than hidden.

## Consequences

- A total atomic teardown now self-heals within ~1 minute instead of staying down
  until a manual reinstall.
- Two mutually-restoring rails: the cost of keeping protection down rises from
  "one atomic wipe" to "wipe both rails at once and keep doing it".
- The path-rotating self-update now maintains **two** binary copies, adding
  moving parts to a delicate flow.
- The watchdog's own liveness becomes something that must be checkable (a
  silently-dead cron watchdog is worse than none).

## Honest limitation

Friction, not a seal. Cron is fragile on modern macOS (Apple is deprecating it;
may need Full Disk Access). The ~1-minute cron granularity leaves a teardown
window long enough to launch a game. Recovery is still **local**: a determined
sudo user can find and remove both rails atomically and repeat it. The durable
commitment weight stays in the **server-side override gate** / an external
accountability holder.

## References
- In-band mesh lineage: `0010-single-mesh-fail-fast.md`
- Fresh-label re-install reused by recovery: FEATURE 10 / `0014-independent-mesh-labels-xor-roster.md`
- Avoided failure mode: ADR-0015 (fetch-storm) — recovery is deliberately local
- Cross-platform OS-seam principle: `../REQUIREMENTS_REGISTER.md` Section 5
