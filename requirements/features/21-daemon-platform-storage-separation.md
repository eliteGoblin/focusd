# FEATURE 21 — Separate the daemon's identity from the platform's storage (a wiped platform folder can't disable the recoverer)

- **Status:** 🟡 **BUILDING — test-mode-tier PASS, NOT shipped** (hardening epic,
  2026-07-14). Built on branch `hardening/hf1-storage-separation` (fix `f0593fb`);
  **sandbox/test-mode-verified (TC-25), NOT live-verified, NOT deployed.** Came from
  a weakness the owner demonstrated **live**. Two live-tier follow-ups + an actual
  deploy remain before this can be marked shipped — see Acceptance criteria + the
  honesty caveat below.
- **Extends:** [FEATURE 17](17-daemon-recovery-resilience.md) (wiped-workdir recovery).
  Pairs with [FEATURE 22](22-recovery-survives-full-teardown.md) (out-of-band seed)
  and [FEATURE 24](24-disguise-plugin-process-identity.md) (disguise the daemon's
  own location).
- **Class of defect it closes:** the §5 self-recovery principle at a **new layer** —
  the recoverer sharing fate with the thing it is supposed to recover.

## Why

Live (2026-07-14) the owner **deleted the platform's working folder** and the
**whole system went down — including the daemon whose job is to rebuild the
platform**. FEATURE 17 made a wiped platform folder recoverable *by the daemon*;
but if the same wipe **also takes the daemon down**, there is no recoverer left to
run. The daemon's own identity/storage is today entangled with the platform's
working folder: destroy the folder and you destroy the recoverer with it. This is
the project's most important principle (self-recovery is non-negotiable, register
§5) broken one layer up.

## What

The daemon keeps its **own identity and storage independent of the platform's
working folder**, and reliably re-establishes the platform after that folder is
deleted.

- **Independent fate.** Deleting the platform's working folder leaves the daemon's
  own identity + storage intact and the daemon still running.
- **Reliable re-establishment.** With the platform folder gone, the daemon detects
  the absence and rebuilds the platform (re-creates its storage, brings the engine
  back) on its own, within a bounded window.
- **Fresh, not a survivor.** Recovery re-creates what it needs; it does not depend
  on any leftover pointer inside the deleted folder.

## How it behaves (product rules)

- **Wiping the platform folder does not remove, disable, or corrupt the daemon.**
- **The daemon rebuilds the platform on its own** after the wipe, bounded time, no
  manual action.
- **The re-established platform is a fresh instance** — recovery relies on nothing
  left inside the deleted folder.
- **No behaviour change when nothing is wrong.**

## Acceptance criteria (strict, observable)

> **Verify ONLY in a sandbox / test-mode instance — never by tearing down the
> owner's real install.** (Register §5 requires a real failure-and-recover path;
> this must be exercised on a disposable instance, not the live commitment device.)

1. **Platform-folder wipe leaves the daemon alive.** In a sandbox/test-mode
   instance, removing the platform's working storage leaves the daemon's own
   identity + storage **present** and the daemon **still running** — no shared-fate
   takedown.
2. **Reliable re-establishment within a bounded window.** After that wipe, the
   daemon re-creates the platform storage and brings the engine back within a
   **bounded, stated time**, with **zero manual action**.
3. **Fresh recovery, not a survivor.** The re-established platform is a **freshly
   created** instance; recovery does not rely on any artifact left inside the
   deleted folder.
4. **No regression when clean.** On an untouched instance, nothing changes.

> **VITAL recovery bar (2026-07-14).** Deleting the working folder (`rm -rf`) must
> not permanently break protection: the supervisor re-establishes it, and the
> recovered artifact is **FRESH** (new mtime / new pid), not a survivor — with **no
> manual help**, within a **bounded, stated window**. This must be **exercised, not
> assumed** — standing live-tier gate **TC-31** (pairs with the process-invisibility
> gate TC-30, since the folder is the obvious `rm -rf` target once found).

### Verification status (2026-07-14)

- **Sandbox / test-mode tier: PASS (TC-25).** Baseline-then-fix on branch
  `hardening/hf1-storage-separation` reproduced the weakness OPEN on master and
  CLOSED on the fix (twice each, clean isolation): daemon survives the wipe +
  re-establishes the platform at a fresh path (criteria 1–3), no regression when
  clean (criterion 4), status truthful, and a decoy real-install dir that `install`
  deleted on master survives on the fix (blast-radius scoped). Reusable script
  `scripts/testmode/TC-25-storage-separation.sh`.
- **Honesty caveat.** Test mode does not auto-relocate the binary, so the baseline
  **emulated** the production single-root shared-fate rather than exercising the real
  relocate. The sandbox proves the separation logic; **live must confirm the real
  relocate.**
- **Live-tier follow-ups (required before shipped):** (1) a real disguised
  generation-workdir under the actual `~/Library` survives a **real** install;
  (2) status stays truthful when a **genuinely-running** platform's workdir is
  deleted (no green-over-dead). Plus an actual deploy.

## Honest limitations

- **Friction, not a seal.** A determined root user can wipe the daemon's own
  storage too — the durable answer to that combined attack is the out-of-band
  recovery seed ([FEATURE 22](22-recovery-survives-full-teardown.md)) and the
  off-box layer (FEATURE 13 / icebox), not this feature alone.
- **A bounded recovery window is a real gap.** There is a stated interval between
  the wipe and the platform being back — enough, in principle, to launch a game.
- **One more location to keep hidden.** The daemon's independent storage is another
  disguised location that must not become an at-a-glance tell (see
  [FEATURE 24](24-disguise-plugin-process-identity.md)).
