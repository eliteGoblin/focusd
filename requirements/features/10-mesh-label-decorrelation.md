# Feature 10 — Decorrelate the self-protection mesh labels + faster self-heal

- **Status:** defining (2026-06-15) · awaiting human gate before build
- **Decision:** [ADR-0014](../decisions/0014-independent-mesh-labels-xor-roster.md)
- **Closes:** the deferred shared-prefix trade-off (GitHub issue #20)
- **Builds on:** [FEATURE 8](08-single-mesh.md) (the single mesh) ·
  [FEATURE 3](../REQUIREMENTS_REGISTER.md) (the XOR-masked pubkey pattern this reuses)

## What

The self-protection mesh is three launchd entries — two always-on workers that
respawn each other, plus a periodic ensurer that acts as a backstop. Today all
three share **one** random base name and differ only by a role-revealing
suffix (`.a` / `.b` / `.ensure`). This feature makes the three look like three
**unrelated** third-party background agents, and makes the mesh heal faster
than a person can dismantle it.

Three changes, all approved and user-locked, kept deliberately KISS:

- **Independent labels per role.** Each of the three mesh roles gets its own
  fully random label (prefix + suffix + tail drawn from the existing disguise
  pool). No shared base. No `.a` / `.b` / `.ensure` (or any other
  role-revealing) token. Nothing in the name tells you which one is the
  backstop, or that the three belong together.
- **A persisted, masked roster.** The three-label roster is written to an
  "encrypted" (XOR-masked) file in the hidden workdir, reusing the same
  casual-grade masking already used for the embedded pubkey (FEATURE 3). The
  in-memory roster is the primary source of truth; the file exists only so a
  freshly relaunched survivor can recover the full roster on a cold start.
- **Tighter self-heal cadence.** The reconcile loop tightens from ~10s to ~2s,
  so single-entry manual removal loses the whack-a-mole race: by the time the
  person removes the third entry, the first is already back.

## Why

focusd is a commitment device, and the adversary is the user's own weak-moment
self. Two weaknesses in today's mesh hand that adversary exactly what they need:

- **Shared prefix is a cluster-find.** Find any one entry, read its prefix,
  search for that prefix, and the other two fall out together. One discovery
  unravels all three.
- **The suffix advertises the structure.** `.a` / `.b` / `.ensure` announces
  that this is a mesh and which entry is the backstop — telling the adversary
  both that there is more to remove and which one to remove last.

Separately, the heal loop ran every ~10s while a person can remove the three
entries one at a time in about ~5s total — so today the **manual removal wins
the race**. Tightening the cadence flips that: a single removal is healed in a
couple of seconds, faster than the next manual removal can complete.

This was previously a known, accepted-for-now trade-off (GitHub issue #20).
This feature closes it.

## How it behaves (product rules)

- **Three unrelated-looking agents.** The three labels share no common
  prefix/stem and carry no role-revealing token. To a casual reader they are
  three independent third-party helpers, not a coordinated set.
- **Memory leads, file recovers.** While any worker is alive, the roster it
  holds in memory is authoritative. The masked workdir file is only read to
  reconstruct the roster when a survivor relaunches cold with nothing in memory.
- **The roster file self-heals.** If the file is deleted, edited, or corrupted,
  a running worker rewrites it from the in-memory roster. Tampering with the
  file never breaks a running mesh.
- **The roster is not casually readable.** A plain `cat` of the file shows
  masked bytes, not plaintext labels.
- **Removal loses the race.** After any single mesh entry is removed, a
  survivor recreates it within a couple of seconds — comfortably inside the
  window a person needs to remove the next one by hand.

## Acceptance criteria (testable behaviour)

1. The three launchd labels **share no common prefix or stem**, contain **no
   `.a` / `.b` / `.ensure`** (or any other role-revealing) token, and are drawn
   from **distinct vendor families** — not all `com.apple.*` (ideally three
   different vendors) — so they do not visually cluster under one
   developer/family to a casual reader.
2. After any single mesh entry is removed (its plist deleted and the job booted
   out), a survivor **recreates it within a few seconds** — the target is well
   under the ~5s a person needs for manual one-at-a-time removal.
3. The persisted roster file is **not human-readable in plaintext**: a casual
   `cat` shows masked bytes. On a cold start with no in-memory roster, the
   daemon **recovers the full roster from this file**.
4. **Tampering with or deleting the roster file does not break a running
   mesh** — a live worker rewrites the file from memory and the mesh continues
   undisturbed.

## Honest limitations

This is **casual-grade friction**, and it is recorded as such — it must not be
over-claimed.

- **What it defeats:** a casual `cat` / `ls` of the workdir; the
  `launchctl … | grep <prefix>` cluster-find that used the shared base; and
  slow, manual, one-at-a-time removal (which now loses the heal race).
- **What it does NOT stop:** reading the daemon binary to recover the XOR key
  and un-mask the roster; invoking the daemon's own un-mask path; or a scripted
  **atomic** teardown that boots out and removes all three entries at once
  (decorrelation raises the cost of *finding* the set, not of removing a set
  you already hold).
- **Login Items grouping is NOT solved (known limitation).** macOS System
  Settings → Login Items → "Allow in the Background" groups background items by
  the app's **signing identity**, and all three mesh roles execute the *same*
  binary. So even with fully decorrelated label text, the three may still appear
  **grouped together in that specific UI**. Separating them there would require
  per-role binary copies — a larger, less-KISS change — so it is recorded here
  as a known limitation for the architect to assess (see Design questions). This
  feature does not claim to defeat the Login-Items grouping.
- The durable commitment weight remains the **server-side override gate**. This
  local layer is friction only — it buys time against an impulsive attempt, not
  certainty against a determined, scripted one.

## Design questions / cross-cutting notes

- The 3-platform Go philosophy still holds. The label-generation and roster
  logic should stay OS-agnostic where it can; the launchd specifics live behind
  the existing per-OS adapter seam. The daemon stays thin.
- Masking reuses the **existing** casual-grade XOR pattern from FEATURE 3 — no
  new crypto, no new dependency. Reuse over reinvention is the deliberate KISS
  call (see ADR-0014 for the alternatives weighed).
- **For the architect:** Can the Login-Items same-signature grouping be
  addressed within KISS (e.g. per-role binary copies), or is it out of scope and
  recorded as a limitation? Decide in DESIGN.
