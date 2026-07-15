# ADR-0023 — Deploy upgrades freely; refuse only a request to STOP the running protection

- **Status:** accepted (2026-07-14) · **STANDING DECISION — do not re-open without
  Frank explicitly re-raising it.**
- **Decided by:** Frank (product owner)
- **Relates to:** register §5 ("Release acceptance — DONE = e2e-verified LIVE + working
  locally", "You can always remove it", "Two deliberate exits") · §8 (deploy mechanics)
  · `../deploy-recipe.md` · ADR-0022 (local-first resilience) · the `focusd-protection`
  refusal rule.

## Context

focusd is a self-protection commitment device. The refusal rule (rightly) makes the
agent refuse to help the weak-moment self **stop / disable / uninstall** the running
protection. But that same caution was bleeding into the **build/deploy** path: shipping
a *stronger* protection build — a hardening fix or a new defensive feature — was being
treated with the same suspicion, as if a deploy might be a disguised teardown. That is
the wrong threat model. Making the protection **harder to remove** is the goal, not a
risk; hesitating to deploy an upgrade slows down exactly the work that serves the
installed intent.

## Decision

1. **Deploy hardening / feature UPGRADES freely.** Building and shipping a stronger
   protection build is *always* aligned with the installed intent. The full path —
   **build → sign → publish a signed release → install → verify live** — proceeds
   without a commitment gate, and **the agent runs the privileged install itself** (it
   holds sudo for the deploy). No ceremony, no second-guessing an upgrade.
2. **Refuse ONLY a request to STOP / disable the CURRENT running protection.** The line
   the agent holds is unchanged and narrow: it refuses to kill / pause / disable /
   uninstall / bypass the protection that is **running right now** (that path stays
   behind the uninstall ritual + the 5-gate override).
3. **Do NOT police whether an upgrade is "secretly a bypass."** It is **not** the
   agent's job to audit a hardening deploy for whether it might weaken something — that
   is **not the threat model**. A build that ships and verifies as stronger is accepted
   on its face; the durable brakes on *removal* are the ritual + the override, not
   friction on *deploys*.

## What this is NOT

This does **not** open a new removal path. "Deploy freely" applies to builds that
**stand up or strengthen** protection. A change whose effect is to **stop the running
protection** is a STOP request (decision #2), no matter how it is framed.

## Alternatives considered

- **Gate every deploy behind the same suspicion as an uninstall.** Rejected — it
  penalises the aligned action (hardening) to guard against a case the ritual/override
  already cover, and it slows the core mission work.
- **Have the agent statically audit each upgrade for hidden weakening.** Rejected — out
  of threat model, brittle, false-positive-prone; "is the running protection being
  stopped?" is the real, checkable question.

## Consequences

- Hardening/feature builds ship on demand; the agent performs the signed release +
  privileged install without a gate (recipe in `../deploy-recipe.md`).
- The refusal surface stays crisp and narrow — **stop-the-running-protection**, not
  "any change to focusd."
- A deploy is still not **DONE** until it clears the live-e2e + working-locally bar
  (§5 release-acceptance) — deploy-freely governs *permission*, not *acceptance*.

## Honest limitation

The distinction relies on reading intent: "upgrade" vs "stop." A sufficiently disguised
weakening deploy could be waved through under "deploy freely." Accepted — the durable
defenses against *removal* remain the uninstall ritual and the 5-gate override, not
deploy-time friction; and any deploy that leaves protection down fails the live-e2e
acceptance gate, so a fake "upgrade" that actually disables cannot be recorded DONE.

## References
- Register: `../REQUIREMENTS_REGISTER.md` §5, §8
- Deploy recipe / env deps: `../deploy-recipe.md`
- Release acceptance evidence: `../e2e-test-history.md`
