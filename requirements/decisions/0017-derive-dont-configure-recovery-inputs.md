# ADR-0017 — Derive, don't configure: deterministic recovery inputs

- **Status:** accepted (2026-06-17)
- **Relates to:** §5 principles "Self-recovery is non-negotiable" + "KISS"; §6 limitation 10
- **Decided by:** Frank (product owner), BA review

## Context

A serious self-heal flaw was found live. The system is supposed to recover every
protection layer without manual intervention, but the protection engine could not
actually re-fetch its own binary on recovery: every rebuild path (first install,
self-update, and the out-of-band watchdog) used the **wrong download identity for
the protection-engine binary**, so the recovery download failed.

It stayed hidden for a long time because the binary had been **placed by hand**,
so the health status read HEALTHY while auto-recovery was in fact broken — a
latent failure, exactly the kind §5 warns against.

Two things went wrong:
1. The download identity was an **operator-supplied, free-form input** — a knob
   for a value that can only ever be one correct thing.
2. The same wrong handling was **copied across every rebuild path**, so there was
   no correct fallback path to mask the others.

## Decision

**For any input to a recovery path that is fully determined by known facts,
derive it once — do not expose it as a configurable knob.**

Concretely for this case: derive the protection-engine binary's download identity
deterministically in one place, remove the free-form knob entirely, and have every
recovery path use that single derived value. With no knob to set wrong and one
shared derivation, every rebuild path is **correct by construction**. Ship the fix
via the in-place self-update so protection never drops during the change.

This generalizes the KISS principle: speculative configurability is a defect
surface. When a value has exactly one correct form, the product computes it; it is
not something an operator (or a future code path) can get wrong.

## Alternatives considered

- **Keep the knob, fix each path's value.** Rejected: it fixes today's symptom but
  leaves the footgun — the next path added can set it wrong again, and the same
  bug can re-appear independently in each path. The knob itself is the defect.
- **Validate the configured value at startup.** Rejected as not-KISS: a guard
  around a knob that should not exist. Deriving the value removes the failure mode
  instead of detecting it after the fact.

## Consequences

- One source of truth for the download identity; every recovery path is correct by
  construction, and a whole class of "wrong identity" failures disappears.
- Less configurability — which is the point: nothing to mis-set.
- Reinforces the verification discipline in §5: recovery is now testable by
  tearing protection down for real and confirming it rebuilds itself, rather than
  trusting a green status that a hand-placed artifact could be propping up.

## Honest limitation

This ADR is a principle plus one applied fix; the fix **shipped and was verified
live** (daemon-v0.5.3): with the engine binary deleted and its process killed,
the system re-fetched the engine and self-healed in ~4s; and after a full
teardown (every supervisor entry + all processes + the work directory wiped) the
out-of-band rail rebuilt everything and re-fetched the engine, recovering in
~45-60s — no manual placement. Deriving this one identity does not retroactively prove every
*other* recovery input is derived rather than configured — those should be reviewed
against this principle. And derivation only removes the *configuration* failure
mode; the recovery path still depends on the source it fetches from being
reachable (see the still-open fetch-path follow-up, §9).

## References
- Self-recovery + KISS principles: `../REQUIREMENTS_REGISTER.md` Section 5
- The live lesson: `../REQUIREMENTS_REGISTER.md` Section 6, limitation 10
- Related open recovery follow-up: `../REQUIREMENTS_REGISTER.md` Section 9
