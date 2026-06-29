# ADR-0020 — Out-of-band rail becomes a separate minimal launchd binary (own folder) that supervises the daemon without Full Disk Access, with an offline signed daemon backup

- **Status:** accepted (2026-06-29) · approved-building
- **Feature:** [FEATURE 18](../features/18-resilient-out-of-band-watchdog.md)
- **Decided by:** Frank (product owner), BA review
- **Supersedes / reverses:** [ADR-0016](0016-out-of-band-watchdog-rail.md) — the
  cron choice for the out-of-band rail. The rail *concept* (a separate, mutually-
  guarding recovery rail) stands; the **cron mechanism** is retired.
- **Relates to:** the **self-recovery is non-negotiable** principle (register §5)
  and the latent-failure lesson (register §6) · FEATURE 17 (daemon-layer wiped-
  workdir recovery).

## Context

ADR-0016 chose **cron** for the out-of-band watchdog, with the honest caveat that
"cron is fragile on modern macOS … may need Full Disk Access." That caveat became
the failure. Confirmed live during a real incident:

- **Modifying the cron schedule on modern macOS requires Full Disk Access (TCC).**
  The launchd-spawned daemon — and **any** automated, non-interactive context —
  lacks that permission, so the schedule write hangs and is killed. The companion
  rail could therefore **neither self-heal nor be scripted-restored**, and it sat
  **DOWN** on the live machine. ADR-0016's "the two rails mutually re-install each
  other" claim was **false in practice** on the real OS.

- **Recovery was network-bound.** ADR-0016 made recovery deliberately *local* (run
  the on-disk second copy, no fetch) — but the engine binary it would rebuild
  still had to be re-fetched if it was gone. With no reachable source, recovery
  stalls.

The rail that is supposed to survive a *total* teardown has to work in an
automated context **without special permissions**, and has to restore the engine
**offline**.

## Decision

**The out-of-band rail is a separate, minimal launchd-managed binary in its own
folder (NOT the daemon binary, NOT under the daemon's workdir) that supervises the
DAEMON without Full Disk Access, carrying a signed offline backup of the daemon so
it can rebuild the daemon with no network.**

- **A separate minimal binary in its own folder — not a copy of the daemon.** The
  point of "out-of-band" is that the rails do not share a fate. If the companion
  shared the daemon's binary or workdir, deleting that folder — the exact incident
  that just took protection down — would destroy **both** rails at once. So the
  companion is its own small, stable binary in its own location, surviving a wipe
  of the daemon's binary/workdir. This **refines the earlier same-binary / "second
  copy of the daemon binary" idea**, which would have shared fate.
- **Supervision chain — companion supervises the daemon (the root of recovery).**
  companion → keeps the **daemon** alive (rebuilds it if gone); daemon → keeps the
  **platform** alive; platform → runs the **plugins**. The companion's only job is
  "is the daemon mesh alive? if not, rebuild it" — minimal, few reasons to change.
- **launchd, not cron.** The companion runs on a mechanism the system can establish
  and repair in an automated context **without Full Disk Access** — the exact thing
  cron could not do.
- **Offline signed daemon backup.** The companion stores its own
  signature-verified copy of the daemon binary (optionally the platform too) and
  **rebuilds the daemon offline** when the daemon binary/workdir is gone — no
  network dependency on the recovery path. The copy is **verified before promotion**,
  so an unsigned/tampered backup is never run.
- **Mutual guarding preserved.** The two rails still re-establish each other; the
  property ADR-0016 wanted is kept, on a mechanism that actually works without FDA.
- **Stays out of generation-cleanup.** The companion is **not a mesh worker**, so
  FEATURE 17's mesh-generation cleanup will not sweep it; the build keeps them
  separate.

## Alternatives considered

- **Keep cron; require the user to grant Full Disk Access to a Terminal once.**
  Rejected: it makes recovery depend on an **interactive, manually-permissioned**
  step the automated daemon can never perform itself — which is precisely the
  latent-failure shape §5 forbids (a rail that looks present but can't self-heal).
  The owner's intent is automated recovery, not a ritual.
- **Keep recovery network-only (re-fetch the engine).** Rejected: it leaves a hard
  dependency on a reachable source at the worst moment. An on-disk signed copy
  restores protection offline; the network path still rolls forward afterward.
- **Drop the out-of-band rail entirely and rely on the in-band mesh + FEATURE
  17.** Rejected: FEATURE 17 closes the wiped-workdir BLOCK at the *daemon* layer,
  but a *total* atomic teardown still needs a survivor on a separate rail. The
  rail's purpose is unchanged; only its mechanism is fixed.

## Consequences

- The out-of-band rail is once again **self-healing on the live machine** —
  closing the deferred TC-05 failure — and now recovers **offline**.
- Reverses ADR-0016's cron decision; ADR-0016 is marked superseded by this ADR.
  The honest "cron is fragile / may need FDA" limitation is now resolved by
  removing cron rather than living with it.
- The self-update flow must keep the companion's signed daemon backup in sync (an
  extra copy to maintain — a moving part, as ADR-0016 already noted for the
  second binary copy). The companion's minimalism keeps this cost low: it changes
  rarely, so the backup it carries is stable.
- The companion is a separate, independently-located binary the build must produce
  and place outside the daemon's folder — and keep **out of** FEATURE 17's
  mesh-generation cleanup (it is not a mesh worker).

## Honest limitation

**Friction, not a seal.** A determined sudo user can still find and remove both
rails atomically and repeat it; this raises the cost, it is not a wall. The
companion's offline copy may lag the latest desired engine version (restores
first, rolls forward later). The durable commitment weight remains the **off-box
layer** — the server-side override gate and the FEATURE 13 heartbeat/accountability
holder (icebox).

## References
- Reversed decision: `0016-out-of-band-watchdog-rail.md`
- Rail concept + total-teardown rationale: FEATURE 12
- Daemon-layer wiped-workdir recovery (companion to this): FEATURE 17
- Self-recovery + latent-failure lesson: `../REQUIREMENTS_REGISTER.md` §5, §6
- Derive-don't-configure / KISS spirit: `0017-derive-dont-configure-recovery-inputs.md`
