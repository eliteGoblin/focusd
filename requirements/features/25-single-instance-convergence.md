# FEATURE 25 — Single-instance convergence + in-place upgrade (one daemon, one platform; no CLI teardown door)

- **Status:** 🟡 **DEFINING — approved-building** (hardening epic, 2026-07-14).
  **NOT built, NOT verified.** Came from a live observation by the owner.
- **Extends:** [FEATURE 17](17-daemon-recovery-resilience.md) (idempotent install +
  generation retirement — "no pileup") and **ADR-0013** (exactly one platform per
  install, enforced by a daemon-held lock). This feature makes those two guarantees
  hold **through an install/upgrade** and **at steady state**, not just on a clean
  fresh install.
- **Build ordering:** shares the install/reconcile build surface with **HF1
  (FEATURE 21)**, **HF2 (FEATURE 22)**, and **HF4 (FEATURE 24)** — so its build
  **sequences after** those land, to avoid churning the same files twice.
- **Grade:** reliability / hygiene, plus one **hardening** rule (criterion 4: no CLI
  teardown door).

## Why

The owner observed **duplicate accumulation** live — at one point **~16 "platform"
processes** running at once — and wants an upgrade to **replace in place** and end
at a guaranteed single-instance state. Two problems compound:

1. **Upgrades stack instead of replacing.** Installing a newer version did not
   reliably retire the prior daemon generation(s) and the old platform(s); old and
   new ran side by side, drifting away from the "exactly one" guarantee that
   FEATURE 17 / ADR-0013 promise. A stale **old-version daemon** left running is
   both a reliability problem and a visible tell.
2. **Any user-invocable "cleanup/stop" verb would double as a teardown vector.**
   The moment convergence/cleanup is exposed as a CLI command a user can type, it
   becomes exactly the impulsive off-switch the whole product exists to deny (the
   "no inside door handle" principle). So the cleanup must be **internal to
   install/reconcile**, never a sanctioned command.

FEATURE 17 already retires stale generations *on install*, but the live pileup shows
that guarantee is not holding across real upgrades and does not re-converge at
**steady state** — a duplicate introduced after install can persist.

## What

On install/upgrade, focusd **brings up the newer daemon+platform, retires ALL old
and duplicate generations, and converges to exactly one daemon + one platform** —
with the old daemon **replaced in place**. The convergence is **entirely in-binary**
(internal to install/reconcile); it is **not** exposed as any user-invocable command.

- **In-place upgrade.** Installing a newer version retires the prior generation(s)
  and replaces the running daemon with the new one — one live generation before and
  after, never a growing stack.
- **Converge to exactly one.** After any install/upgrade, exactly **one** daemon
  generation and **one** platform process are live — no orphans, no stale
  old-version daemon.
- **Steady-state re-convergence.** If a second/duplicate platform appears **after**
  install (however it got there), the reconcile loop **retires it on its own** — the
  single-instance guarantee is continuous, not a one-shot at install.
- **No teardown door.** The cleanup is internal. There is **no** CLI verb a user can
  invoke to stop / clean up / kill protection. The **only** sanctioned removal
  remains the existing uninstall ritual.

## How it behaves (product rules)

- **Upgrade replaces, never stacks.** After installing a newer version, the old
  daemon is gone and the new one is running in its place — count of live generations
  stays at one.
- **One daemon, one platform, always** — after any amount of install/upgrade churn.
- **A planted duplicate gets reaped.** Introduce a second platform at steady state
  and the reconcile loop returns the system to exactly one, unprompted.
- **No stop/cleanup command exists.** The user cannot type anything that stops or
  tears down protection; only the ritual removes it.
- **No behaviour change** to what protection enforces; no regression to recovery /
  self-heal.

## Acceptance criteria (strict, observable)

> **Verify ONLY in a sandbox / test-mode instance — never against the owner's real
> install.** Test-mode-tier verifiable.

1. **Single instance after install/upgrade.** After an install/upgrade there is
   **exactly one** daemon generation **and one** platform process — no orphans, no
   old-version daemon left running.
2. **In-place replacement.** Installing over an existing install **retires the prior
   generation(s)** and replaces the daemon **in place** — not a second stack beside
   the old one.
3. **Steady-state re-convergence.** A deliberately-introduced second/duplicate
   platform is **retired by the reconcile loop at steady state**, not only at
   install time.
4. **No CLI teardown door.** There is **no** user-invocable command that stops,
   cleans up, or kills protection — the convergence logic is internal to
   install/reconcile only; the **uninstall ritual** is the sole sanctioned removal.
5. **No regression.** Recovery / self-heal behaviour (FEATURE 17 / 18) is unchanged;
   protection enforcement is unchanged.

## Honest limitations

- **Friction, not a seal.** A root user can spawn duplicates faster than the
  reconcile loop reaps them, or race an in-place upgrade. This turns unbounded
  pileup into a **self-correcting, single-instance steady state** — it does not stop
  a determined sudo user in the moment. Durable commitment weight remains the
  off-box / server-side layer (FEATURE 13 / icebox).
- **A transient duplicate window is expected during handover.** During an upgrade or
  a singleton handover there can be a brief overlap before the old instance is
  retired (consistent with ADR-0013's crash-orphan edge). "Exactly one" is the
  **steady-state** guarantee, not an instantaneous invariant.
- **Depends on the retirement path being able to see old generations.** This inherits
  FEATURE 17's follow-up lesson: a generation whose binary was wiped could be
  invisible to cleanup — the retire step must treat such dead generations as
  reapable (that fix direction lives in the FEATURE 17 follow-up).
- **Consolidation coupling with HF4 (FEATURE 24).** F25 and HF4 land together in the
  hardening-epic consolidation. Once HF4 disguises the platform's identity, F25's
  orphan-reaper must recognise the platform by its **disguised** identity — the reap
  signature has to be updated in lockstep, or convergence won't see (or will
  mis-target) the very process it's meant to keep at exactly one.
