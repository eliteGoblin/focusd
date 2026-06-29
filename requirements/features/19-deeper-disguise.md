# FEATURE 19 — Deeper disguise (kill the obvious tells the owner found)

- **Status:** ✅ shipped + live-verified (daemon-v0.5.7, PR #73, 2026-06-29).
  The mesh role/marker moved off the command line into the plist environment, and
  the supervisor labels were given distinct non-clustering naming styles.
  **Live-verified (TC-20):** e2e-verifier confirmed `ps` for the mesh marker + role
  flags returns **0** hits and the labels do not cluster as a near-identical
  triplet. **Residual CLOSED:** the stale-records cleanup overlap with FEATURE 17's
  generation cleanup is resolved by the orphan-sweep (daemon-v0.5.8) — e2e TC-21
  PASS (1 state.db + 1 platform after churn, re-confirmed daemon-v0.5.10).
  **One open follow-up remains** — the watchdog/rebuild path re-leaks the mesh role
  on argv (e2e TC-23, bug #83; see below).
- **Builds on:** [FEATURE 10](10-mesh-label-decorrelation.md) (decorrelated mesh
  labels) · [FEATURE 14](14-mesh-argv-leak-minimization.md) (argv leak
  minimization — this extends that work). Picks up the **iceboxed stale-records
  cleanup** noted as a residual in FEATURE 14.
- **Maps to:** e2e-test-history **TC-20** (no at-a-glance tells); the stale-record
  cleanup overlaps **TC-21** (post-recovery convergence, open).

## Why

During the live incident the owner spotted the install at a glance. Three tells
made it obvious, and each is friction the disguise is *supposed* to provide:

1. **The mesh role is visible in the process list.** A mesh process's command
   line still shows its mesh role / marker, so a plain `ps aux | grep mesh` (or a
   grep for the role flags) finds the install immediately — undercutting FEATURE
   14's work to strip the command line.
2. **The supervisor entries look like triplets.** The login/background entries
   read as **three near-identical** `com.vendor.X.<hex>` names. The owner's words:
   "3 look very similar, obvious." Clustered, same-shape names defeat the point of
   decorrelation.
3. **Stale records accumulate.** Prior generations leave behind a pile of old
   login-items / background-task records (the owner sees **many**) — clutter that
   both advertises the install and is a maintenance smell.

This is **friction grade** (register §5): disguise defeats casual `grep`/`ls`, not
a determined reader of the source. The goal here is only to remove the *obvious*
at-a-glance tells the owner actually hit.

> All identifiers in this spec are **examples only** — `com.vendor.X.<hex>`,
> `mesh`, the role flags — not the real install's tokens.

## What

Reduce the obvious tells so a casual look does not reveal the install.

- **Hide the mesh role/marker from the process list.** A mesh process's *visible*
  command line should not reveal its mesh role or marker — these move off the
  command line (into a place the process list does not display) — so a grep for
  "mesh" or the role flags against the live process list **finds nothing**.
- **Use varied, plausible names — not a visible triplet.** Replace the three
  near-identical `com.vendor.X.<hex>`-style entries with **varied, plausible
  single- or two-word names** that **do not cluster** — so the entries don't read
  as an obvious matching set.
- **Clean up stale records.** Remove the old login-items / background-task records
  left behind by prior generations, so the install doesn't accumulate a visible
  trail. (Complements FEATURE 17's generation cleanup.)

## How it behaves (product rules)

- **Grep-for-mesh comes up empty.** Searching the live process list for the mesh
  marker or the role flags returns nothing.
- **Entries don't look like a set.** The supervisor/login entries have varied,
  unrelated, plausible names — not three near-identical hex-suffixed siblings.
- **No leftover trail.** Stale entries from superseded generations are cleaned up.

## Acceptance criteria (observable)

1. **No mesh tell in the process list.** A search of the live process list for the
   mesh marker **and** for the role flags returns **nothing** for the live mesh.
2. **Names don't cluster.** The login/background entries are **not** three
   near-identical `com.vendor.X.<hex>` names — they are varied, plausible names
   that don't read as a matching set.
3. **Stale records cleaned.** Old login-items / background-task records from prior
   generations are removed.

## Follow-up (open) — the rebuild/watchdog path still leaks the mesh role on argv

`[found 2026-06-29, live]` FEATURE 19 moved the mesh role/marker off the command
line **for the `daemon install` path**, but the **watchdog/rebuild path** (used by
the FEATURE 18 companion and the legacy recovery) does **not** route through the same
env-based setup — so after an out-of-band recovery the rebuilt mesh shows
`run --r a --mesh` / `run --r b --mesh` on the visible command line again, and a
`grep`-for-mesh works once more. A clean `daemon install` clears it (leak = 0).

- **Impact:** the at-a-glance `ps` tell returns **only** after an out-of-band
  recovery, and only until the next clean install. Friction/hygiene grade, **not** a
  bypass — protection stays healthy.
- **Fix direction:** route the watchdog/rebuild mesh-install through the same
  FEATURE 19 env-carried plist path that `daemon install` already uses, so every
  rebuild path is leak-free by construction.
- **Maps to:** e2e **TC-23** (after companion/watchdog rebuild, `ps | grep mesh`
  returns 0 — FAIL until fixed), **bug #83**.
- **Confirmed with decisive evidence** `[daemon-v0.5.10, 2026-06-29]`: a direct
  read of the rebuilt plists shows mesh plists with the visible `--mesh` role
  (count = 2) and **zero** carrying the F19 env marker — the rebuild emits
  **pre-F19-format** plists. Two theories were **refuted**: it is **not** test-mode
  (the prod daemon rejects the test flag; the release was built without the e2e tag)
  and **not** a stale backup (the backup already carries F19). **Root cause not yet
  pinned** — tracked under #83.

## Honest limitations

- **`argv[0]` stays visible to root.** The binary's own path is always shown by
  the process list to a root user — the OS gives no way to hide it. This is
  **friction, not invisibility**: it removes the at-a-glance role/marker tell and
  the obvious-triplet tell, but a determined root reader can still find the
  binary by its path. (Same honest limit as FEATURE 14.)
- **Disguise never resists reading the source.** Per register §5, naming/disguise
  defeats casual `grep`/`ls`, not Claude-reading-the-source or a determined
  reverse-engineer. The durable defenses are time/real-world-state/behavioral, not
  obscurity.
