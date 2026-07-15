# FEATURE 23 — Continuous plugin authenticity (re-verify every cycle; refuse & re-fetch a tampered binary)

- **Status:** 🟡 **DEFINING — approved-building** (hardening epic, 2026-07-14).
  **NOT built, NOT verified.** Came from a weakness the owner demonstrated **live**.
- **Extends / reconciles:** [FEATURE 15](15-plugin-binary-integrity.md) (plugin
  binary integrity). **⚠️ Tension to resolve with the human before build** — see
  Honest limitations: F15 is recorded *shipped + live-verified* as closing exactly
  this class, yet the live demonstration shows a dummy binary **persisting**.
- **Class of defect it closes:** the **false-green enforcement bypass** — a dead
  stand-in plugin running in place of the genuine one while protection reads healthy
  (register §5, §6 limitation 11).

## Why

Live the owner **swapped plugin binaries with dummy binaries and they persisted and
ran** — because, in the demonstrated behaviour, authenticity is effectively checked
**at install time, not continuously**. A dummy binary that sits in place means the
corresponding protection is **silently dead**. This is the single cheapest, quietest
bypass of the enforcement layer and the project's most dangerous failure shape — a
protection that is gone while the system reports it present.

## What

Each plugin's authenticity is **re-verified every reconcile cycle** — not trusted
from a one-time install check — and a plugin that fails is **refused and replaced
from the signed source of genuine plugins** before anything runs.

- **Re-verify every cycle.** On every reconcile pass, each plugin binary is checked
  for authenticity against the signed genuine reference — continuously, not once.
- **Refuse + re-fetch.** A binary that fails authenticity is **refused (never run)**
  and **replaced from the signed source**; the genuine binary is what actually runs.
- **Tamper is a recorded event.** The refusal + replacement is written as a durable,
  observable audit record — never a silent repair.

## How it behaves (product rules)

- **Authentic-or-refused, every cycle** — a match runs; a mismatch is refused and
  replaced from the signed source, then the genuine one runs.
- **A dummy binary cannot persist** across cycles — it is superseded by the genuine
  binary.
- **Never run the substitute** in place of the genuine plugin.
- **Tamper is recorded** (audit), consistent with the observability principle.
- **No behaviour change when clean.**

## Acceptance criteria (strict, observable)

> **Verify ONLY in a sandbox / test-mode instance — never by tampering with the
> owner's real install.**

1. **Per-cycle re-verify (not install-time only).** A plugin binary replaced with a
   different (dummy) binary is **detected on the next reconcile cycle** — not only at
   install/startup.
2. **Refuse + re-fetch the genuine one.** The tampered binary is **refused (never
   run)** and **replaced from the signed source**; the genuine binary is what
   actually runs.
3. **Dummy binary cannot persist.** After detection, the dummy does **not** remain in
   place across cycles — it is superseded by the genuine binary within a **bounded
   number of cycles**.
4. **Tamper recorded.** Each detection produces a durable, observable audit record.
5. **No false green.** While a plugin is tampered/unrestored, health does **not**
   read ok for that plugin.
6. **No regression when clean.** On an untampered system, plugins run normally with
   no spurious tamper events.

## Honest limitations

- **⚠️ Overlaps FEATURE 15 — reconcile with the human first.** F15 is recorded
  *shipped + live-verified* as promoting plugin binaries to "verify every reconcile
  tick + restore on mismatch". The live demonstration that a **dummy binary
  persisted and ran** means F15's per-tick restore either **regressed** or **never
  actually covered this path**. Whether F23 is a *new feature* or a *bug against
  F15* is a decision for the human — do not pre-decide in build.
- **Friction + fast detection, not a seal.** Root can re-tamper or race the
  replacement in a tight loop; this makes an *impulsive* swap fail fast and loud, not
  a scripted attacker impossible.
- **Binary authenticity only.** Plugin **config/policy** integrity (blocklists,
  target-app lists, schedule) remains the **separate iceboxed follow-up** (e2e
  TC-10) — a genuine binary pointed at a gutted config is a different hole.
