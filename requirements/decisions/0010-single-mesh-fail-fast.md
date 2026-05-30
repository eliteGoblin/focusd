# ADR-0010 — Single mesh, operator-choice install, fail-fast

- **Status:** accepted (2026-05-30)
- **Supersedes:** ADR-0009 (dual-mesh runtime)
- **Decided by:** Frank (product owner) + architect review + BA review

## Context

To keep the Claude-side skill re-injected on a schedule, the system briefly
ran **two protection engines at once** — one with admin power, one as the
user. It worked, but it doubled the moving parts: two engines, two state
stores, two install footprints to keep aligned, and (looking ahead) two things
a future server would have to track for one person. The product owner's
reaction: *"seems not KISS"* — and the deeper worry from past experience,
*"avoid confusing like the model was implemented I only caught up later."*

## Decision

**One protection engine per install. Never two at once.** The operator chooses
the install flavour up front:

- **Admin install** → full power. Runs every protection. The behavioural
  (user-level) protection is handled by the single engine temporarily stepping
  down to the user's identity only for that task.
- **User install** → deliberately limited fallback for machines without admin
  rights. Runs only the behavioural protection. The admin-level protections
  are reported as **unavailable**, not broken.

**Fail fast, no surprises.** If the admin install can't get admin rights, it
**stops and says so** — it does NOT quietly fall back to the limited user
install. The operator decides to switch, explicitly. (User's words: *"if sudo
install not success, do not try to auto install as user. fail and let user
switch to usermode."* and *"KISS fail fast. not trying being too smart. not
surprise user."*)

## Alternatives considered

- **Keep dual engines (ADR-0009).** Rejected: doubles complexity, two state
  stores drift over time, harder for a future server to reason about one user.
- **Single user engine + ask-for-admin-per-action.** Rejected: would need broad
  standing admin grants — a wider security surface than stepping down per task.
- **Auto-fall-back to user install when admin fails.** Rejected explicitly:
  silent downgrade is exactly the "surprise" the owner wants to avoid.

## Consequences

- Simpler to reason about, run, and keep alive; one identity for a future server.
- **Accepted trade-off:** if the single engine crashes, all protections pause
  for the few seconds it takes to auto-restart (previously the two engines
  failed independently). Judged acceptable for the simplicity win.
- "User mode is a degraded fallback, not a feature to support extra things" is
  now a standing principle — see philosophy.

## References
- Feature spec: `../features/08-single-mesh.md`
- Superseded: `0009-dual-mesh.md`
