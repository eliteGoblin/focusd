# FEATURE 22 — Recovery survives a full manual teardown (out-of-band seed + redundant starts)

- **Status:** 🟡 **DEFINING — approved-building** (hardening epic, 2026-07-14).
  **NOT built, NOT verified.** Came from a weakness the owner demonstrated **live**.
- **Extends:** [FEATURE 18](18-resilient-out-of-band-watchdog.md) (the out-of-band
  companion rail). **Relates to open issue #87.**
- **Class of defect it closes:** the combined teardown — disable the restart
  mechanism **and** wipe the working folder — that defeats a recovery whose only
  pointers live inside the things being removed.

## Why

Live the owner **disabled auto-start (the login item) AND deleted the platform
folder** — the combination stopped everything and did **not reliably recover**.
FEATURE 18's companion handles a total *in-band* teardown, but a combined "turn off
the restart path + wipe the folder" still wins if (a) every recovery pointer lives
inside something the teardown removes, or (b) there is a single start mechanism to
toggle off. Recovery has to survive the owner deliberately turning the restart
machinery off and wiping state at the same time. (Register §5: self-recovery is
non-negotiable; §6 limitation 12 is the incident lineage this extends.)

## What

An **out-of-band recovery seed not referenced by any running process**, plus
**redundant start mechanisms**, so protection re-establishes after the combined
teardown — **fresh**, within a bounded window.

- **A seed that is out of band.** The recovery seed is **not pointed to by any live
  process**, so it can't be found by inspecting what's running and removed along
  with everything else.
- **Redundant starts.** No single toggle disables every path back — disabling one
  auto-start mechanism still leaves an independent path that can bring protection
  back.
- **Fresh re-establishment.** After the combined teardown, protection is **rebuilt
  from the seed** — a freshly rebuilt install, not a process that merely dodged the
  teardown.

## How it behaves (product rules)

- **Seed is not process-referenced** — a teardown that enumerates the running
  system does not find it.
- **No single kill-switch** — disabling any one start mechanism does not prevent
  recovery.
- **Recovered protection is freshly rebuilt from the seed**, not a survivor.
- **Comes back within a bounded, stated window.**

## Acceptance criteria (strict, observable)

> **Verify ONLY in a sandbox / test-mode instance — never by tearing down the
> owner's real install.**

1. **Combined teardown recovers.** In a sandbox/test-mode instance, disabling the
   auto-start path **AND** removing the platform folder still results in protection
   **re-establishing within a bounded, stated window**, with no manual action.
2. **Seed is not process-referenced.** The recovery seed is **not discoverable by
   enumerating running processes / their references** — confirm the seed survives a
   teardown that removes everything reachable from the running system.
3. **Fresh, not a survivor.** The recovered install is demonstrably a **freshly
   rebuilt instance**, not a process that merely escaped the teardown.
4. **Redundant starts.** Disabling **any single** start mechanism does not prevent
   recovery — at least one independent path remains.

## Honest limitations

- **Friction, not a seal.** A root user who finds and removes the seed **and** all
  rails atomically still wins locally. The durable commitment weight remains the
  **off-box layer** (FEATURE 13 heartbeat/accountability + the server-side override
  gate / icebox).
- **The bounded recovery window is real freedom-time** before protection is back.
- **The seed is one more thing to keep disguised** (FEATURE 24) and to keep from
  decaying into a stale tell.
- **Relates to issue #87** — track the combined-teardown recovery gap there.
