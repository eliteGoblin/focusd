# FEATURE 26 — Disguise quality: BLEND-IN (the install reads as ordinary, unrelated apps)

- **Status:** 🟡 **DEFINING** (disguise-quality bar, 2026-07-15) — captured, **not
  yet scheduled**; needs the human gate to slot it into the hardening consolidation.
  Sharpens the disguise, does not change enforcement.
- **Extends:** [FEATURE 19](19-deeper-disguise.md) (killed the at-a-glance *process*
  tells) and [FEATURE 24](24-disguise-plugin-process-identity.md) / HF4 (nothing
  greppable in the running system maps to the working folder). This carries the same
  discipline to the **on-disk naming quality** of the install's folders + binaries.
- **Grade:** friction / hygiene (register §5) — defeats a **casual survey** of the
  apps folder, not a determined reader of the source.

> All identifiers in this spec are **examples only** — not the real install's tokens.

## Why

Even once the process table is clean (FEATURE 24 / TC-30 met live), the install still
sits on disk under the apps folder as a set of folders and binaries. Today those names
share a recognizable **shape** — a common vendor-style prefix plus a **hex/UUID
suffix** (example only: `.com.<x>.<x>.<hex>`) — and the **same shape repeats** across
the daemon's home folder, the platform's working folder, and the binaries.

That shared shape is a **signature**. A single eye-pattern, or one `grep`/`ls` over the
apps folder, can pick out **all** of the install's folders at once, and the hex suffix
reads as "not a normal app." The whole point of the disguise is to blend in with the
real vendor entries around it; a matching-set signature defeats that. This is the same
class of tell FEATURE 19 removed from the process list, now at the on-disk layer.

## What

Make the on-disk install read like **ordinary, unrelated apps**.

- **Real-looking, word-based names — no hex/UUID suffixes.** Names are built from
  random **words** and look like the kind of folder a normal Mac app leaves behind —
  not a machine-generated `…<hex>` token.
- **No shared pattern across the install's folders.** The daemon's home folder, the
  platform's working folder, and the binaries must **not** share a common prefix,
  suffix, or token that ties them together — so **no single grep/eye-pattern
  identifies all of the install's folders as a set**.
- **Each install / each folder differs.** No two folders in one install (and no two
  installs) reuse the same name shape.

## How it behaves (product rules)

- **A survey of the apps folder can't single the install out.** Scanning the apps
  folder by eye or with a `grep` doesn't flag the install's folders as anomalous or as
  a matching cluster — they sit among the real entries without standing out.
- **No hex-suffix tell.** Names read as plausible apps, not machine-generated strings.
- **No behaviour change** to what focusd enforces.

## Acceptance criteria (observable)

> Verify in a **sandbox / test-mode** instance first, then a **read-only live survey**
> — never tear down the owner's real install.

1. **Not anomalous to a survey.** A casual `grep`/`ls` survey of the apps folder
   **cannot** pattern-match the install as anomalous, and **cannot** pick the install's
   folders out as a matching set.
2. **Names look like ordinary apps.** Each folder/binary name reads like a plausible
   real app — word-based, no hex/UUID suffix, no "not-a-real-app" tell.
3. **Each install/folder differs.** No shared token/prefix/suffix ties the install's
   folders together, and the name shape is not reused install-to-install.

## Honest limitations

- **The executable path stays root-visible.** A root user can still read a running
  process's own executable path (e.g. via `lsof`) and follow it to the folder — the
  same honest ceiling as FEATURE 14 / 19 / 24. This raises the cost of a **casual
  apps-folder survey**; it is **not** concealment.
- **Disguise never resists reading the source** (register §5). Casual-grade friction
  only — it defeats `grep`/`ls`, not a determined reverse-engineer.
- **This is naming quality, not a new location.** *Where* the install lives (still the
  apps folder) is a **separate, iceboxed** idea ("Install location beyond Application
  Support"). This feature only fixes how the names **read**, not where they **sit** —
  the two stack, they don't substitute.

## Open design questions (for the human gate)

- **Schedule now or after v0.18.0's VITAL bars land?** Slot into the hardening
  consolidation, or hold until TC-30/TC-31/TC-32 are all clean?
- **Does path-rotating self-update adopt the same scheme?** Self-update already
  generates fresh names on rotation — confirm rotation produces word-based,
  no-shared-pattern names too, so "each install differs" survives a rotation.
