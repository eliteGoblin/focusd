# FEATURE 17 — Daemon recovery resilience (survive a wiped workdir + stop generation pileup)

- **Status:** 🔨 defining / approved-building (product owner approved 2026-06-29)
- **Closes:** a live incident where protection went **fully down and did not
  auto-recover** (see register §6, the workdir-delete + generation-pileup limits).
- **Builds on:** [FEATURE 12](12-out-of-band-watchdog.md) (out-of-band recovery —
  the companion rail is FEATURE 18) · the **self-recovery is non-negotiable**
  principle (register §5).
- **Maps to:** e2e-test-history teardown-matrix TCs (TC-14 workdir delete, TC-17
  generation pileup, TC-18 combination).

## Why

The whole system promises that **every protection layer recovers without manual
intervention after any single failure** (register §5). A live incident proved
that promise was false for two real attack shapes:

1. **A wiped workdir is a permanent BLOCK, not a recovery.** The "which platform
   version do I want?" answer lived **only** inside the workdir's own state. So
   when the owner deleted the workdir, the daemon had no desired version to aim
   at, logged "BLOCKED: no desired version", and **never re-fetched the
   platform** — protection stayed down and games ran. The thing that knows how to
   recover stored its recovery target inside the very thing that got wiped.

2. **Installs pile up stale generations.** Repeated installs / recoveries left
   **many** stacked-up generations on disk (a live count found 6 workdirs and 14
   disguised supervisor entries). That breaks the single-instance guarantee
   (two platform processes were found running at once) and leaves a growing trail
   of clutter that is both a reliability problem and a visible tell.

The old "daemon recovers the platform" test only ever deleted the platform
**binary** — and the state (the desired version) survived that, so recovery
worked. The **workdir-delete** path was never tested, so this was a **latent
failure**: the exact class §5 exists to prevent.

## What

The daemon recovers the platform **even when its workdir and on-disk state are
gone**, and installs **stop accumulating stale generations**.

- **A fallback platform version baked into the daemon itself.** The daemon
  carries a known-good platform version inside its own binary. If no desired
  version can be read (because the workdir/state was wiped), the daemon **falls
  back to the baked version, re-fetches the platform, and runs it** — instead of
  blocking forever. A wiped workdir becomes a recoverable event, not a dead end.
- **Idempotent install + a global singleton.** Installing or recovering must
  converge to **exactly one** live generation — not stack a new one on top of the
  old. Install **cleans up / supersedes prior generations** as part of its own
  run, so re-installing repeatedly is safe and leaves no pileup. Exactly one
  platform supervisor runs regardless of how much generation churn happened.

## How it behaves (product rules)

- **Wiped workdir self-heals.** With the workdir/state deleted, the daemon does
  not block on "no desired version" — it adopts the baked fallback, re-fetches,
  and brings the platform back on its own, then rolls forward to the latest
  desired version when it can.
- **Re-install is convergent, not additive.** Running install again (or a
  recovery running it) results in one healthy generation, with prior generations
  retired — never two-or-more live side by side.
- **One platform, always.** After any amount of install/recover churn there is
  exactly **one** platform process and **one** live supervisor generation.
- **No pileup.** Old workdirs / disguised supervisor entries from superseded
  generations are cleaned up, not left to accumulate.

## Acceptance criteria (observable)

1. **Wiped-workdir recovery.** After the workdir is deleted, protection
   **auto-recovers within a bounded time** with no manual action — there is **no
   permanent BLOCK** on "no desired version".
2. **Single live generation.** After repeated installs and/or recoveries, there
   is **exactly one** platform process and **one** live supervisor generation.
3. **No stale-generation pileup.** Superseded generations (old workdirs /
   disguised supervisor entries) are cleaned up; the on-disk count does not grow
   with each install/recover.

## Honest limitations

- **The baked fallback can be older than the last desired version.** Recovery
  brings protection back at the baked version first, then **re-fetches and rolls
  forward** to the latest desired version — so there can be a brief window on an
  older-but-working engine before it catches up.
- **Root can still delete faster than recovery.** A determined sudo user can wipe
  the workdir repeatedly and race the recovery. This is **friction, not a seal** —
  it turns a permanent outage into a bounded, self-healing blip. Durable
  commitment weight remains the server-side override gate / off-box layer
  (FEATURE 13 / icebox).
- **Recovery still depends on the fetch source being reachable** to roll forward
  past the baked version (the baked version alone restores protection offline at
  the daemon layer; the companion's offline binary backup is FEATURE 18).
