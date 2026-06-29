# FEATURE 18 — Resilient out-of-band watchdog (companion that works without Full Disk Access + recovers offline)

- **Status:** ✅ **shipped + live-verified** (daemon-v0.5.9, PRs #78/#80, 2026-06-29).
  **Phase 1 (daemon-offline recovery) shipped:** a separate launchd companion in its
  own folder carries a signed offline backup of the daemon; on a stale heartbeat it
  rebuilds the daemon (and thus the mesh + platform) from that backup with **no
  network and no Full Disk Access**. **Live-verified:** the entire in-band rail was
  removed (daemon down, 0 platforms, 0 mesh plists) and the companion detected the
  stale heartbeat and **rebuilt the daemon from its signed offline backup → mesh +
  platform back**. Maps to **TC-16 / TC-17 (PASS)**. **Phase 2 (platform-offline
  backup) deferred:** the companion currently carries the daemon backup only; the
  daemon re-fetches the platform (network) — an offline *platform* restore is a
  later phase.
- **Decision:** [ADR-0020](../decisions/0020-launchd-out-of-band-rail-no-fda.md)
  — **reverses ADR-0016's cron choice** (cron → launchd out-of-band rail).
- **Supersedes:** the deferred **TC-05** (the cron rail that could neither
  self-heal nor be scripted-restored without Full Disk Access).
- **Replaces the rail of:** [FEATURE 12](12-out-of-band-watchdog.md) (the
  out-of-band watchdog concept stays; the cron *mechanism* is retired).

## Why

FEATURE 12 added an out-of-band recovery rail — a "companion in another folder"
that rebuilds the protection mesh after a total teardown. It was deployed on
**cron**. The live incident proved the cron rail is unreliable in exactly the
moment it is needed:

- **The cron rail needs Full Disk Access to exist.** On modern macOS, writing the
  recovery schedule requires a permission (Full Disk Access) that the daemon — and
  any automated, non-interactive context — **does not have**. Confirmed live: the
  write hangs and is killed. So the companion could **neither self-heal nor be
  scripted-restored**; it sat **DOWN** on the live machine. A watchdog you think
  guards you but that has silently stopped is worse than none.

- **Recovery still depended on the network.** A wiped engine had to be re-fetched
  to come back. If the fetch source is unreachable (or the owner is offline),
  recovery stalls.

The companion is the rail that is supposed to survive a *total* teardown. It has
to work in an automated context **without** special permissions, and it has to be
able to bring the engine back **offline**.

## What

The out-of-band recovery rail is a **separate, minimal companion** that lives in
its **own folder** (NOT the daemon's binary, NOT under the daemon's workdir),
works **without Full Disk Access**, and can rebuild the daemon **offline**.

- **A separate, minimal binary in its own folder — not a copy of the daemon.** The
  whole point of "out-of-band" is that the two rails do not share a fate. If the
  companion shared the daemon's binary or workdir, deleting that folder — *exactly
  the incident that just took protection down* — would destroy **both** rails at
  once. So the companion is its **own small binary in its own location**, surviving
  a wipe of the daemon's binary/workdir.
- **A supervision chain — each layer keeps the next alive.** Recovery is layered,
  with the companion as the **root of recovery**:
  - **companion** (separate binary, own folder, launchd-managed, no FDA) → ensures
    the **daemon** is alive; if the daemon/mesh is gone, it re-installs and brings
    it back.
  - **daemon** → ensures the **platform** is alive.
  - **platform** → runs the **plugins** (the actual enforcement).
- **Minimal and set-and-forget.** The companion's only job is "is the daemon mesh
  alive? if not, rebuild it." Few reasons to change → it rarely needs updating →
  it stays stable and out of the way.
- **A signed offline backup of the daemon.** The companion carries a
  **signature-verified copy of the daemon binary** (and enough to rebuild) in its
  own folder, so it can **rebuild the daemon with no network** when the daemon
  binary/workdir is deleted. It may optionally also carry a signed platform backup.
  The backup is signature-checked before it is ever promoted, so an offline restore
  can't be poisoned.
- **launchd, not cron.** The companion runs on a rail the system can **establish
  and repair in an automated context without Full Disk Access** — retiring the cron
  rail that needed a permission no automated context has.

## How it behaves (product rules)

- **Survives deletion of the daemon's folder.** Wiping the daemon binary and
  workdir does **not** remove or disable the companion — it lives elsewhere. The
  companion notices the daemon/mesh is absent and rebuilds it.
- **Rebuilds the daemon offline.** With the daemon binary, the mesh, and the
  workdir all gone, the companion restores the daemon from its **own on-disk signed
  copy** — even with no network. The daemon then brings the platform back, and the
  platform runs the plugins.
- **Establishes and repairs itself with no special permission.** The companion rail
  can be set up and put back if removed, in an automated context, **without Full
  Disk Access** — the failure that left TC-05 down can't recur.
- **Verified before promotion.** The companion's stored daemon copy is
  signature-checked before it is used to restore — an unsigned/tampered copy is
  never promoted.
- **Mutually re-installs with the main rail.** Because a launchd job can be toggled
  off, the rails re-establish each other and run disguised/separately; this is
  friction, not a seal (see limits + FEATURE 13's off-box lock).

## Acceptance criteria (testable behaviour)

1. **Deleting the daemon does NOT disable the companion.** With the daemon binary
   **and** its workdir deleted, the companion is **still present and running**, and
   it **rebuilds the daemon offline within a bounded time** — no manual action.
2. **Offline total-teardown recovery.** With the main mesh, the daemon binary, and
   the workdir all torn down, the supervision chain comes back **OFFLINE** (no
   network fetch) within a bounded time: companion → daemon → platform → plugins.
3. **No Full Disk Access required.** The companion can be **installed and
   self-healed in an automated, non-interactive context without Full Disk
   Access** — it does not depend on an interactive Terminal that has been granted
   a special permission.
4. **Signed-before-restore.** The companion's stored daemon copy is
   signature-verified before it is promoted; an unsigned/tampered copy is refused.
5. **Companion is not swept by generation-cleanup.** FEATURE 17's pileup cleanup
   only retires **mesh supervisor generations**; the companion is deliberately
   **not a mesh worker**, so cleanup never touches it (see note below).
6. **Supersedes TC-05.** The cron-rail-can't-self-heal failure is closed: the
   out-of-band rail is present and self-healing on the live machine again.

## Honest limitations

- **A determined root user can still remove both rails.** This is **friction, not
  a seal** — it raises the cost of keeping protection down (you must wipe the main
  rail **and** the companion, repeatedly), but a sudo user who finds and removes
  both atomically still wins locally. The **durable lock remains the off-box
  layer** (FEATURE 13 heartbeat/accountability + the server-side override gate /
  icebox).
- **Offline recovery restores the backed-up version.** The companion's offline
  copy of the daemon may be older than the latest desired version; it restores
  protection first, then the normal update path rolls forward when a network is
  available.
- **The companion's rebuild path re-introduces an at-a-glance tell.** After the
  companion rebuilds the daemon, the rebuilt mesh shows its role marker on the
  visible command line again (the rebuild/watchdog path does not yet use FEATURE 19's
  env-carried hiding) — so a `grep`-for-mesh briefly works again until the next clean
  install clears it. Hygiene/friction grade, not a bypass; tracked as the open
  follow-up in [FEATURE 19](19-deeper-disguise.md) (e2e TC-23, **bug #83**).
  **Confirmed with decisive evidence** (daemon-v0.5.10, 2026-06-29): a direct read
  of the rebuilt plists shows the pre-F19 (visible-role) format, not test-mode and
  not a stale backup — **root cause not yet pinned**.
- **Reverses an accepted decision.** This deliberately reverses ADR-0016's choice
  of cron — recorded in ADR-0020, with the live FDA evidence as the rationale. It
  also refines the earlier "same daemon binary / second copy" framing: the
  companion is a **separate minimal binary in its own folder**, not a second copy
  of the daemon binary under shared fate.

## Note — interaction with FEATURE 17 (generation cleanup)

FEATURE 17 stops generation pileup by cleaning up superseded **mesh supervisor
generations**. The companion is **deliberately not a mesh worker**, so that
cleanup naturally does **not** sweep it away. The build must keep them separate:
the companion is recognised as out-of-band, not as a mesh generation to retire.
