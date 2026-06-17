# Feature 12 — Out-of-band watchdog (close the total-atomic-teardown gap)

- **Status:** ✅ shipped (2026-06-17) · merged to master in **PR #52** · deployed live + battle-tested
- **Decision:** [ADR-0016](../decisions/0016-out-of-band-watchdog-rail.md)
- **Closes:** the one proven residual of the in-band mesh — a **total atomic
  teardown** leaves no survivor to rebuild.
- **Builds on:** [FEATURE 8](08-single-mesh.md) (the single self-protecting mesh) ·
  [FEATURE 10](10-mesh-label-decorrelation.md) (decorrelated mesh labels +
  fresh-label re-install, which this watchdog reuses).

## Why

focusd is a commitment device, and the adversary is the user's own weak-moment
self. The in-band self-protection mesh — two always-on workers that respawn each
other plus one periodic ensurer that acts as a backstop — is battle-tested and
recovers in ≤2s from every **partial** attack: kill a process, `killall`, delete
a single plist, disable-then-kill, even a sustained 241-pass kill-storm. All of
these have been verified live and all heal.

The one proven residual is a **total atomic teardown**: delete all three plists,
kill all the processes, and wipe the workdir — all at once. That leaves no
survivor, so the mesh stays down. The ensurer that would normally rebuild is
itself an in-band member of the mesh, so it dies with everything else. Today
**nothing survives a total wipe to rebuild the protection.** A weak-moment self
who scripts this once is free until they reinstall by hand.

This feature closes that gap by putting a recovery agent on a **separate rail**
that a mesh-only teardown does not touch.

## What

An **out-of-band watchdog** that lives outside the mesh and re-installs the mesh
after a total wipe. User-locked design — Option A: KISS, **local** recovery.

- **A separate rail.** The watchdog is triggered by **cron**, not by launchd —
  a different scheduling mechanism that a mesh wipe does not remove. Cron was
  empirically confirmed to fire on this machine.
- **Same binary, second copy.** No new program. The watchdog is the *same*
  daemon binary, placed as a **second copy** at a separate disguised location
  **outside the mesh workdir**, so wiping the workdir does not remove it. A root
  cron entry runs it every minute via a new `daemon watchdog` subcommand: check
  whether a healthy mesh is installed; if not, run the existing install path
  (fresh labels, per FEATURE 10).
- **Local recovery, no network.** The watchdog recovers from the copy already on
  disk — it does **not** fetch anything. This is deliberate: it avoids the
  fetch-storm failure mode recorded in ADR-0015.
- **One-shot install.** `daemon install` is streamlined to set up everything in
  one command: the mesh **plus** the watchdog binary copy **plus** the cron entry.
- **Mutual guarding.** The mesh's own reconcile also re-installs the cron entry
  and the watchdog copy if either is removed. Two rails guard each other: to keep
  protection down you must wipe **both** rails at once — and keep doing it.
- **Stays in sync.** Self-update refreshes the watchdog copy too, so a stale copy
  self-corrects on the next update.

## How it behaves (product rules)

- **Total teardown self-heals.** After all three plists, all processes, and the
  workdir are gone, the watchdog notices the mesh is absent on its next run and
  rebuilds a fresh, healthy mesh — no manual action.
- **Two rails, mutually restored.** Remove the watchdog while the mesh is alive
  and the mesh puts it back; wipe the mesh while the watchdog rail survives and
  the watchdog puts the mesh back.
- **One command installs both rails.** A single `daemon install` stands up the
  mesh, the watchdog copy, and the cron entry.
- **Recovery is local and quiet.** The watchdog runs the on-disk copy; it makes
  no network request to recover.

## Acceptance criteria (testable behaviour)

1. After a **total teardown** of the mesh (all three plists deleted **and** all
   processes killed **and** the workdir removed), the mesh is **rebuilt and
   healthy within the watchdog interval (~1 minute)**, with no manual action.
2. Removing the watchdog (its cron entry and/or its binary copy) while the mesh
   is alive → the **mesh re-installs the watchdog**.
3. A single `daemon install` sets up the mesh **and** the watchdog rail **and**
   the binary copy — one command, no extra steps.
4. The watchdog uses the **same daemon binary** (no separate program) and
   recovers **locally** (no network fetch).

## Verified outcomes (live install + battle-test, 2026-06-17, PR #52)

This shipped and was exercised on the live system. What was confirmed:

- **One-command install set up everything in one shot.** A single
  `sudo daemon install -v <ver>` stood up the mesh (3/3), the platform
  (v0.15.0, reporting good), **and** the out-of-band watchdog (the cron rail
  present) — no extra steps. **Acceptance #3 and #4 verified.**
- **Status abstracts the mechanism.** `daemon status` shows every component and
  reports the watchdog as **"out-of-band watchdog: present"** — it never names
  cron or any underlying mechanism. This addresses the "the watchdog's own
  health must itself be checkable" limit below: its presence is now a
  redaction-safe line in the status view.
- **Total teardown self-healed (battle-test). Acceptance #1 verified live.** A
  *total* atomic teardown — all three plists deleted **and** all processes
  killed **and** the mesh workdir wiped, i.e. fully down — was **rebuilt by the
  out-of-band watchdog in ~5 seconds**. (This run happened to land just before a
  cron minute boundary; worst case is still ~60s per the recorded ~1-minute
  limit.) The mesh came back 3/3, the platform recovered, and overall health
  returned to HEALTHY with no manual action.
- **Reviewed before merge.** Went through go-reviewer + security-reviewer; **two
  HIGH findings were fixed before merge**: (1) an empty-version guard so the
  install can't write a broken cron entry, and (2) the watchdog copy is
  Ed25519-verified before any root reinstall (no unsigned second copy can be
  promoted to root).

## Honest limitations

These were recorded at design time and **held in practice** — the live
battle-test did not invalidate any of them. This raises the cost of a total
teardown; it is **not** an absolute seal. Record all of these explicitly — do
not over-claim.

- **Cron is fragile on modern macOS.** Apple is deprecating cron and it can need
  Full Disk Access to run — even though it fired cleanly on this machine. A
  watchdog you *think* guards you but that has silently stopped firing is worse
  than none, so the watchdog's own health must itself be checkable.
- **~1-minute recovery window.** Cron granularity means a total teardown leaves
  protection down for up to a minute — enough time to launch a game. This is
  friction, not a seal.
- **Still local.** A determined sudo user can find and remove **both** rails (the
  cron line, the binary copy, and the mesh) atomically, and repeat it. This
  raises the cost; it is not a wall. The durable commitment weight remains the
  **server-side override gate** / an external accountability holder.
- **Adds complexity to a delicate path.** The path-rotating self-update now has
  to maintain **two** binary copies in sync, adding moving parts to an already
  subtle update flow.
- **Resurrection-vs-uninstall footgun (accepted).** The same rail that defeats a
  total teardown also means a half-removed install can resurrect itself. The
  legitimate `uninstall` ritual best-effort-removes the watchdog rail, but a
  partial/manual removal that misses the rail can be rebuilt by it. Accepted: the
  supported way out is the gated uninstall, not a manual wipe.

## Design questions / cross-cutting notes (for the architect — decide in DESIGN)

- **Where does the watchdog copy live?** Its disguised location must survive a
  workdir wipe (i.e. it cannot be inside the mesh workdir). Pick a plausible,
  separate path.
- **How is the cron entry disguised** so it does not read as "focusd watchdog"
  to a casual `crontab -l`?
- **How does the mutual re-install avoid thrash/loop** between the two rails (no
  ping-pong where each rail keeps rewriting the other)?
- **How does self-update keep the second copy in sync** across a path rotation
  without leaving an orphaned stale copy?
- **What does the watchdog verify** — only the daemon mesh, or also the platform?
- **3-platform note.** Cron is unix-only, so the rail itself is **darwin-specific
  behind the osadapter seam**; keep the watchdog *logic* (is-the-mesh-healthy /
  run-install) OS-agnostic where it can be. The daemon stays thin.
