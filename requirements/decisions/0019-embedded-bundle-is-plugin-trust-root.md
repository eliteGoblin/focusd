# ADR-0019 — The embedded plugin copy inside the signed platform binary is the plugin trust root; plugin integrity is reconciled every loop, not placed once

- **Status:** accepted (2026-06-22) · shipped platform v0.16.0/v0.16.1 (PRs #62/#63), live-verified 2026-06-22
- **Feature:** [FEATURE 15](../features/15-plugin-binary-integrity.md)
- **Decided by:** Frank (product owner), BA review
- **Relates to:** Ed25519 signed releases (register §4) — reuses that trust root
  rather than minting a new one · the self-recovery principle and the
  false-green / latent-failure lesson (register §5, §6)

## Context

A live finding, demonstrated by the product owner: the platform unpacks the
genuine plugin programs to disk **once at startup**, and the reconcile loop then
runs **whatever program is on disk** without ever re-checking it. Anyone who can
write to disk — on this machine, the weak-moment owner is root — can swap a plugin
program for a do-nothing stand-in. The corresponding protection then silently
dies and **stays dead until the next platform restart**. If the stand-in exits
cleanly, status reports that plugin "ok" — a **false green over dead protection**.
Replacing the Steam-killer this way disabled Steam/Dota enforcement live while
status still looked healthy.

This is the highest-severity flaw found to date: it is a **direct enforcement
bypass of the entire plugin layer**, and it is an instance of the project's most
dangerous failure shape — protection gone while the system reports it present
(register §5 self-recovery, §6).

The root cause is a **placement mistake**, mirroring ADR-0018's: the genuine
plugin programs were treated as a one-time startup placement, while the
continuously-reconciled signed desired state covered only the blocking *policy*,
not the *programs that enforce it*. The disk was trusted as authoritative when it
is the very thing the adversary controls.

## Decision

**Treat the genuine plugin copy embedded inside the Ed25519-signed platform
binary as the plugin trust root, and reconcile each plugin program's integrity
against it on every loop — not once at startup. A detected mismatch is a recorded
security event that is repaired, never a silent fix.**

- **No new keys, no new PKI.** The genuine plugin programs already travel inside
  the platform binary, which is already Ed25519-signed and already verified
  before it is allowed to run. That embedded copy is therefore **already the
  trusted golden reference.** Authenticity = the on-disk plugin matches the
  genuine embedded copy. We reuse the existing release-signing trust root rather
  than inventing a second one.
- **Reconcile every tick, and at point of use.** On every reconcile pass — and
  again immediately before a plugin is run — the on-disk program is checked
  against the genuine copy. Plugin programs join the **continuously-enforced
  signed desired state**, alongside the blocking policy.
- **Restore, atomically, then run.** On a mismatch the platform restores the
  genuine program as a single all-or-nothing swap (safe even if the plugin is
  mid-run) and runs the genuine version.
- **Tamper is an event, not a silent repair.** A mismatch is **recorded as a
  security event** — a log line (FEATURE 16) plus a platform event in the state DB
  (the audit channel). The false-green is closed by the **restore mechanism**
  (point-of-use verify restores the genuine binary before it runs), not by holding
  a tamper flag in status.

This makes plugin programs self-recovering on their own (register §5: "recovered
must mean the system re-created what it needed on its own") and closes the
false-green class for plugin binaries.

> **Refinement (2026-06-22):** This ADR originally said the tamper is "surfaced in
> status" and that status "can never again read a found-tampered plugin as a plain
> ok." That has been corrected. **`status` reflects CURRENT state only (KISS):** a
> plugin that was tampered and has since been auto-restored is genuine and
> enforcing now, so it truthfully reads **ok** — a currently-unrestored tamper or
> a real run error reads **not-ok**. The tamper **history** is audit, not status:
> it lives in the app log (FEATURE 16) and as a platform event in the state DB,
> readable by a future accountability/dashboard view (FEATURE 13 / icebox). The
> integrity guarantee is unchanged — false-green is prevented by restore-before-run,
> not by a persistent status verdict. Owner intent: "status means current state…
> the history can go into the log/events — don't mix things up." See FEATURE 15
> "How it behaves" and TC-07.

## Alternatives considered

- **Keep placing plugins once at startup (status quo).** Rejected: it is the
  defect — a single overwrite kills a protection until restart, with a green
  light over it.
- **Mint a new signing key / per-plugin signatures.** Rejected as not-KISS and
  redundant: the embedded copy inside the already-signed platform binary is
  already a trustworthy golden reference; a second key is more surface, more
  footgun, no extra assurance.
- **Verify only on the loop, never at point of use.** Considered; leaves a window
  between a swap and the next pass during which the stale/substitute program
  could run. The tighter point-of-use check is preferred; the exact balance and
  the residual check-to-exec window are a DESIGN question (FEATURE 15), to be
  named honestly, not papered over.
- **Link the plugins into the signed platform binary and run them from there
  (remove the on-disk surface entirely).** A genuinely stronger structural
  option — there would be no separate on-disk program to overwrite. Deferred, not
  rejected: it is a larger architectural change (build shape, isolation,
  cross-platform packaging) recorded as the strategic alternative for the
  architect to weigh against reconcile-and-repair. This ADR does not commit to it.

## Consequences

- A weak-moment overwrite of a plugin program is **restored within ≤1 reconcile
  tick** and **recorded** — the "dead until restart" window and the false-green
  over a swapped binary both close.
- Plugin programs become part of the signed desired state, correct-by-
  construction in the same spirit as the rest of the reconcile spine — no new
  trust root, no operator knob.
- No behaviour change on an untampered system; the feature is invisible until
  disk disagrees with the genuine copy.

## Honest limitation

This is **friction + fast self-heal + detection, not an unbreakable seal.** Root
can re-tamper immediately or race the restore; this ADR does not stop a
determined scripted root attacker — it makes an *impulsive* swap fail fast and
loud. It covers plugin **binary** integrity only; **plugin config/policy
integrity** (blocklists, target apps, schedule, and a tighten-only "no inside
door handle" for plugin config) is a **related but separate** concern, iceboxed
as a follow-up — this decision must not be described as protecting plugin policy.
The durable commitment weight remains the server-side override gate.

## References
- Reused trust root: Ed25519 signed releases — register §4
- Self-recovery + false-green / latent-failure lesson — register §5, §6 (esp. limitation 10)
- Placement-mistake sibling (authority in the wrong place): `0018-roster-source-of-truth-off-argv.md`
- KISS / derive-don't-configure spirit: `0017-derive-dont-configure-recovery-inputs.md`
- Follow-up (plugin config/policy integrity): `../icebox.md`
