# FocusD — Defect Lessons (project-specific)

> **A standing ledger of every real defect found in focusd → its root cause → the
> fix that closed it → how it stays closed.** The prevention column is the point:
> each defect leaves behind a reusable guard (a regression test-case, an acceptance
> rule, or a design rule) so the same class can't quietly come back.

## How this doc works

- **Scope: project-specific.** These are focusd defects, tied to real features,
  versions, and test-cases. The **general** engineering lessons distilled from the
  same work live in the ECC doc `hardening-verification-lessons.md` (portable across
  any integrity / resilience / verification project). Each entry below **cross-links**
  to the ECC general lesson it instantiates, by name.
- **Standing practice.** When a real defect is found: record it here (project-specific)
  **and** extract the portable lesson to ECC. Every defect must end with a *prevention*
  that another agent can verify against — usually a `TC-*` in `e2e-test-history.md`.
- **Truthful status.** A prevention is only "closed" when its TC is **live-verified**.
  Where a fix is built-but-not-yet-live, or a bar is currently FAILing, it says so.
- **Uncommitted per convention** (requirements branch); no secrets, no disguised tokens.

**Status key:** ✅ closed (live-verified) · 🟡 fix built, live-verification owed ·
🔴 open / currently failing.

---

## Quick map (defect → prevention → general lesson)

| # | Defect (short) | Prevention (TC / rule) | Status | ECC general lesson |
|---|---|---|---|---|
| 1 | daemon ran the platform unverified after download | TC-33 verify-before-exec | ✅ | #1 point-of-use, symmetric layers |
| 2 | unsigned config could soften a protection ("inside door handle") | TC: workdir config inert | 🟡 | #2 guarantee on the live path |
| 3 | tighten-only guarantee asserted in dead code | rule: guarantee on the running path | ✅ | #2 guarantee on the live path |
| 4 | version tag greppable at rest → surfaced the folder | TC-36 disguise blend-in | 🔴 | #3 test accepted limits vs the real attack |
| 5 | every disguised folder shared one `.prefix.hex` shape | TC-36 not-a-matching-set | 🔴 | #6 blend into the population |
| 6 | wiped working folder didn't proactively self-heal | TC-31 fresh-artifact recovery | 🔴 | #4 verify the resource, not the PID |
| 7 | "backup broken" status was a stale reader | rule: migrate observability with mechanism | ✅ | #5 stale reader / migrate metrics |
| 8 | a passing test never exercised the leak vector | TC-30 + "verified = exercised" | ✅ | #8 adversarial exercise / verified=exercised |

---

## 1. daemon → platform anti-tamper gap — the engine ran unverified
**Status: ✅ closed (live) · General lesson #1 (integrity at point-of-use, symmetric across layers)**

- **Defect.** The daemon keeps the protection engine (platform) alive, but it only
  checked the platform's signature **at download** — never **before running it**. A
  platform binary swapped on disk after download executed as "the platform," unverified.
- **Root cause.** Asymmetry in the layered-supervision chain: the engine→plugin boundary
  verified before every run, but the daemon→engine boundary above it verified only at
  fetch. "Fetch-verify once, then trust the on-disk copy forever" is a latent tamper hole.
- **Fix.** Verify-before-exec: the daemon now re-checks the engine's signature
  immediately before launching it, and **reverts a tampered binary to the genuine
  signed one before it can run** (Layered-supervision anti-tamper; consolidated
  hardening build **v0.18.0**).
- **Prevention.** **TC-33** (tampered platform → reverted, not run) — VITAL anti-tamper
  gate, **PASS live 2026-07-15** (restart path). Standing rule: *every* supervision
  boundary verifies before *every* use — hunt for the asymmetric boundary.

## 2. Config-soften — an "inside door handle" on the live path
**Status: 🟡 fix built · General lesson #2 (a guarantee must live on the code path that runs)**

- **Defect.** An unsigned config file in the working folder could **disable or weaken**
  a protection, and a run-mode override could **unschedule all enforced jobs** — an
  "inside door handle" that loosens enforcement from inside the box, which the design
  explicitly forbids (tighten-only, "no inside door handle").
- **Root cause.** Policy could be sourced from an **unsigned, locally-editable** on-disk
  file that the live reconcile path honored — the loosen path existed on the code that
  actually runs.
- **Fix.** Config lock B — **drop the workdir override entirely**; policy is the
  **signed embedded default only**; network-block baked enabled. No local edit can
  weaken policy. (Future direction: policy served from the server.)
- **Prevention.** TC — a neutered/edited on-disk config is **inert** (has no effect).
  Related standing gate: **TC-10** (plugin config/policy integrity — still icebox).
  Rule: enforcement policy must come from a **signed** source, never a locally-editable file.

## 3. Guarantee living in dead code
**Status: ✅ closed · General lesson #2 (guarantee on the running path, not aspirational/dead code)**

- **Defect.** The "no inside door handle / tighten-only" guarantee was asserted inside an
  **unused** reconcile package, while the **live** config path (defect 2) could still
  loosen a protection. A reviewer reading the guarantee where it was written would
  believe it; the running path violated it. (The twin of defect 2.)
- **Root cause.** The assertion — and its test — lived in a module nothing imports, i.e.
  aspirational/dead code, not on the enforcement path.
- **Fix.** Deleted the dead package; moved the tighten-only guarantee onto the **live
  path** (paired with defect 2's config lock).
- **Prevention.** Rule: **grep for real callers before trusting any "can't be weakened"
  claim**; put — and test — the guarantee where enforcement actually happens.

## 4. Version tag greppable at rest → surfaced the disguised folder
**Status: 🔴 open (F26 defining) · General lesson #3 (test accepted limitations against the real attack)**

- **Defect.** The release **version tag** was written into on-disk file content (and into
  folder/binary names), so `grep -r <version>` over the apps-support tree **surfaced the
  disguised folder and printed its path** — defeating the disguise. The accepted limit
  "the version is only reachable from inside the folder" was simply wrong.
- **Root cause.** An "accepted limitation" was **never exercised against the actual
  attack** (a grep from *outside*). Assumptions about reachability are routinely wrong.
- **Fix (in progress).** **F26 disguise-blend** — **mask the version at rest** and use
  names that carry no greppable version/shared token, so a grep for the tag at rest
  returns nothing. (The *process-table* version leak — the same class in the running
  system — is already closed: **TC-30 PASS live 2026-07-15**.)
- **Prevention.** **TC-36** (disguise blend-in) — TO VERIFY (F26 DEFINING). Standing rule:
  every "reachable only from inside" claim is **exercised** with the outside attack
  (`grep -r`, `find`) before it's banked.

## 5. Disguise naming signature — one glob caught the whole install
**Status: 🔴 open (F26 defining) · General lesson #6 (blend into the population, not a uniform pattern)**

- **Defect.** Every disguised folder used the **same shape** (`.<prefix>.<hex>`), so a
  single glob (the hidden-dot, or the hex tail) picked out the **entire install as a
  matching set** — and the hex suffix reads as "not a real app." The uniform obfuscation
  signature *was* the tell.
- **Root cause.** A consistent obfuscation pattern is detectable — uniformity is the
  fingerprint. Real hiding blends into the **diverse** surrounding population.
- **Fix (in progress).** **F26** — **per-folder, word-based, real-app-lookalike names**;
  **no shared pattern** across the install's folders (no single grep identifies them as a
  set); each install/folder differs.
- **Prevention.** **TC-36** (the install isn't a matching set / not anomalous to an
  apps-folder survey) — TO VERIFY. Standing rule: anti-fingerprint = **blend in**, never a
  uniform self-hiding pattern (a lone marker nothing else uses is *more* detectable).

## 6. Self-recovery gap — green over a dead resource
**Status: 🔴 open (VITAL bar, FAILs live) · General lesson #4 (verify the resource, not process liveness)**

- **Defect.** `rm -rf` of the working folder did **not proactively heal**. The daemon
  checked "is the process alive?" — not "is the working folder intact?" — so the engine
  limped on the deleted resource and protection stayed **silently degraded for minutes**
  with no error log.
- **Root cause.** The health/recovery check verified **process liveness, not the
  resource**. A process holds the unlinked inode and reports alive while its state is gone
  — green over dead resource.
- **Fix.** A **working-folder-intact check** on the reconcile tick → proactive
  restart + rebuild + log when the folder is gone, rather than waiting for the next
  restart (FEATURE 21 / FEATURE 17 line).
- **Prevention.** **TC-31** (deleting the working folder self-recovers a **FRESH** artifact
  — new mtime/pid — within a bounded window, no manual help) — VITAL recovery gate.
  **Currently FAIL live 2026-07-15** (v0.18.0 self-heals only on the *next restart*, not
  proactively) — **stays open until live-proven**. Rule: recovery bars are **exercised**,
  never assumed; check the resource, not the PID.

## 7. Stale status reader — a "broken backup" that wasn't
**Status: ✅ closed (the stale-reader defect) · General lesson #5 (migrate observability with the mechanism)**

- **Defect.** `watchdog_copy_ok=false` looked like the offline-backup rail was broken —
  but it was a **false alarm**: status was reading the **superseded cron rail**, not the
  F18 companion, whose backup verified fine. A "broken" status was a **stale reader**.
- **Root cause.** When the mechanism was superseded (cron watchdog → launchd companion),
  its **status/metric was not migrated** with it, so the field read false on the new path.
- **Fix.** Point the status read at the **companion rail** (F18), so the metric reflects
  the live mechanism instead of the retired one.
- **Prevention.** Rule: when a mechanism is superseded, **migrate its status/metrics too**;
  and treat a "broken" status as **possibly a stale reader** — verify the real resource
  before "fixing" a non-bug. *(Honest cross-ref: the same line is now tracking a
  **genuine** breakage — **TC-35, live FAIL 2026-07-15**, the companion's backup really is
  invalid — which is exactly why you verify the actual thing before concluding.)*

## 8. False-green in test coverage — a passing test that never ran the attack
**Status: ✅ closed · General lesson #8 (adversarial exercise / "verified = exercised")**

- **Defect.** **TC-28 "passed"** without ever exercising the **env-dump vector**: the
  workdir had merely moved from an argv flag to an **env var that `ps -E` still prints**,
  so the disguise leak survived — a latent failure hiding **behind a green test**.
- **Root cause.** The test asserted the criterion without performing the **actual attack
  path** (the env dump). "Passed" meant "one path looked right," not "the exploit was run
  and observed to fail."
- **Fix.** **TC-30** splits the check into **argv AND env** vectors and runs the owner's
  exact probes (`ps`, `ps -E`, `ps -o comm`, `find`, `lsof`); acceptance discipline
  recorded as **"verified = exercised."** **TC-30 PASS live 2026-07-15.**
- **Prevention.** **TC-30** (process-table & casual-probe invisibility — nothing locates
  the workdir; no `--workdir` in argv **or** env). Standing acceptance rule: a criterion
  is not verified until the **exact adversary move is performed and observed to fail** —
  every resilience/concealment claim gets a red-team pass that actually performs the attack.

---

*Companion to the ECC general-lessons doc `hardening-verification-lessons.md` (portable
principles). This doc is the focusd-specific ledger: defect → root cause → fix → the guard
that keeps it closed. Add a row whenever a real defect is found; close a row only on a
live-verified TC. Last updated: 2026-07-15.*
