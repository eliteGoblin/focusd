# ADR-0022 — Local-agent resilience + friction; no off-device server (for now)

- **Status:** accepted (2026-07-14) · **STANDING DECISION — do not re-open
  without Frank explicitly re-raising it.**
- **Decided by:** Frank (product owner)
- **Relates to:** register §3 (threat model) · §5 ("You can always remove it",
  "Friction, not cryptography") · §9 (2026-07-14 milestone) · the HF1–HF4 + F25
  hardening epic (FEATURES 21–25) · icebox "Server-managed enforcement mode"
  (now **deferred** by this ADR) · FEATURE 22 / HF2 (self-reinstall, approval-gated).

## Context

Repeated reviews (red-team + live e2e) reconfirmed a fact that is inherent, not a
bug: a launchd-based local guard is **fundamentally findable** on a machine the
owner fully controls. A determined admin — and the owner *is* root on their own
(unmanaged, non-MDM) Mac — can locate the working folder in roughly **5 minutes**
by inspecting running processes and open files.

That keeps re-surfacing one question: *"should we move the guard/authority off the
device to a server, so the weak-moment self can't shortcut it locally?"* It lives
in the icebox as "Server-managed enforcement mode." Frank considers the question
**settled** and was frustrated it got re-asked as if open. This ADR fixes the
answer so it is not re-litigated.

## Decision

1. **NO off-device server — for now.** A server would only ever be a
   **config / lock MANAGER**. It does **not** solve the core problem: the guard
   that actually blocks apps/sites is **inherently local**, and on your own Mac the
   owner can always stop a local process. A server does **not** make the local
   agent run more resiliently. It **may be revisited LATER**, but is **not** being
   built now.
2. **Direction = local-agent resilience + friction.** Build effort goes into making
   the *local* agent robust: **fast, reliable respawn when killed**; **no
   duplicate/old process after an upgrade**; **every recovery rail working**; plus
   **cheap disguise/friction**. (This is the HF1–HF4 + F25 hardening epic.)

## Accepted ceiling (honest limitation, NOT a defect)

On-device disguise is **friction, not concealment.** A determined admin can find
the workdir in ~5 min via process + open-file inspection. This is **ACCEPTED** —
Frank's long-standing "friction, not impossibility" stance. It must **not** be
filed as an open bug or re-litigated; the friction ladder (find → kill → delete →
swap → wait out the 24h) is the defense, no single rung is a seal.

## Out of scope / approval-gated

"**Survive a full delete**" — the agent reinstalls itself after deletion — is the
**malware-flagged persistence pattern** (FEATURE 22 / HF2). It requires the formal
**approval/exemption path** before it could ever be built, so it is **explicitly
OUT of scope now.**

## Alternatives considered

- **Off-device server owning the lock/policy** (icebox "Server-managed enforcement
  mode"). **Deferred, not built.** Even framed as a policy/lock authority with
  fail-closed + cooldown tokens, it does not make the *local* guard un-stoppable —
  enforcement stays local. Kept in the icebox for a possible later revisit; this
  ADR is the standing reason it is off the plan now.
- **MDM / work-style device management.** Rejected: it's your own machine, so
  there is no authority to lock it under (already in §5).
- **Self-reinstall after full delete (true un-removability).** Approval-gated —
  malware-persistence pattern; see FEATURE 22.

## Consequences

- Build effort goes to **local resilience + friction** (HF1–HF4, F25), not a server.
- The **~5-min findability ceiling is accepted and documented**; it is not a ticket
  and is not to be re-argued.
- The icebox server idea is marked **deferred-by-this-ADR**, so it is not re-pitched
  as an open question.
- **SETTLED:** do not re-open without Frank explicitly re-raising it.

## Honest limitation

This keeps focusd purely local, so the strongest *theoretical* commitment lever
(removing disable authority from the machine entirely) is deliberately **not**
pursued right now. The bet stands: **friction + the 24h override delay** defeat the
*in-the-moment* relapse, which is the actual failure mode.

## References
- Register: `../REQUIREMENTS_REGISTER.md` §5, §9
- Icebox: `../icebox.md` "Server-managed enforcement mode" (deferred by this ADR)
- Approval-gated self-reinstall: `../features/22-recovery-survives-full-teardown.md`
