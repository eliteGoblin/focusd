# Feature 08 — Single-engine install (admin or user)

- **Status:** ✅ shipped (daemon + platform releases; mesh self-heal verified on hardware 2026-05-31 per register §6)
- **Decision:** [ADR-0010](../decisions/0010-single-mesh-fail-fast.md)

## What

focusd runs as **one protection engine**, installed in one of two flavours the
operator chooses:

| Install | How you install it | What it protects |
|---|---|---|
| **Admin (full)** | with admin rights | Everything — site blocking, game-killing, packet filtering, AND the Claude-skill re-injection |
| **User (limited)** | without admin rights | Only the Claude-skill re-injection. The rest are reported **unavailable** |

Never two engines at once.

## Why

Simpler to run, keep alive, and reason about — one engine, one state, one
identity. Sets up cleanly for a future server that should see one user, not two.
The previous approach ran two engines side by side; that was judged not-KISS.

## How it behaves (product rules)

- **Admin install does it all.** For the one task that must act as the user
  (writing the Claude skill into the user's home), the engine temporarily steps
  down to the user's identity. The user never sees admin-owned files in their
  own home folder.
- **User install is an honest, limited fallback.** It exists for machines where
  the operator can't get admin rights. It does not pretend to do the admin-level
  protections — it shows them as unavailable, with a hint to reinstall with
  admin rights for full coverage.
- **Fail fast, no surprises.** If an admin install can't get admin rights, it
  stops and tells the operator. It never silently downgrades to the limited
  user install — switching is the operator's explicit choice.

## Acceptance criteria (testable behaviour)

1. With an admin install, the Claude-skill re-injection still runs on schedule
   and re-creates the skill files if deleted (within the normal window).
2. The skill files written under an admin install are owned by the **user**, and
   the user can read/edit/delete them normally (no admin needed).
3. With a user install, the admin-level protections report **unavailable**
   (not "failed", not silent) — and the skill re-injection still works.
4. An admin install that cannot obtain admin rights **exits with a clear error**
   and does NOT fall back to a user install.
5. During a moment when no one is logged in at the screen, the engine does NOT
   mis-write the user's files to the wrong place — it waits and tries again.

## Honest limitations

- If the single engine crashes, all protections pause for the few seconds it
  takes to auto-restart. (Previously the two engines failed independently.)
  Accepted trade for the simplicity win.
- Assumes one primary person at the keyboard. A shared/multi-user Mac is out of
  scope (see philosophy — focusd is a single-user personal commitment device).

## Supersedes
The dual-engine behaviour from FEATURE 1.6 (#37). That fix is replaced, not
removed from history — see ADR-0009.
