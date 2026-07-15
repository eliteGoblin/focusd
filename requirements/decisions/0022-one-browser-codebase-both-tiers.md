# ADR-0022 — One browser codebase, two tiers: the enforced plugin, a standalone self-daemon, and the script fallback

- **Status:** accepted (2026-07-15) · **built** (FEATURE 27 "browser-monitor Both",
  branch `feat/browser-standalone`) — enforced-plugin path now bundled + enabled by
  default; standalone self-daemon built; **live-verification pending**
- **Feature:** [FEATURE 20](../features/20-mac-browser-guard.md) (the script fallback) ·
  FEATURE 27 "browser-monitor Both" (this ADR)
- **Decided by:** Frank (product owner), BA review
- **Amends / supersedes-in-part:** [ADR-0021](0021-coverage-tiers-enforced-vs-utility-fallback.md).
  The two-tier model (enforced vs utility/fallback) **stands**. What changes: (1) the
  utility/fallback tier now has **two** instances, not one; and (2) the tier boundary
  is no longer "which codebase" — the **same** browser codebase now serves both tiers,
  depending on how it is launched. ADR-0021's rejected alternative "fold the fallback
  into the browser-monitor plugin" is the specific point reversed.
- **Relates to:** register §5 (enforced vs utility/fallback tier; friction-not-
  cryptography) · the `browser-monitor` plugin (the enforced browser path) ·
  PR branch `feat/browser-standalone`.

## Context

ADR-0021 drew a clean line: an **enforced tier** (signed, platform-driven, tamper-
resistant) and a **utility/fallback tier** whose first and only instance was
**mac-browser-guard** — a standalone user-mode script for the one machine where the
enforced stack **can't** be installed (a locked-down/app-allowlisting corporate Mac).
At the time these were two separate codebases with two separately-maintained blocklists.

Two problems surfaced:

- **Drift between the two blocklists.** The enforced browser path and the script
  fallback each carried their own list of blocked sites. Two hand-maintained lists
  silently diverge — a site blocked in one place, open in the other — the exact honesty
  failure the register warns against.
- **A gap ADR-0021 didn't foresee: the personal Mac with no enforced platform.**
  ADR-0021 framed the fallback as "for the machine where the stack *can't* run." But
  there is a middle case — a **personal** Mac where the stack simply *isn't* installed
  (yet, or by choice). The single-file script self-heals only against casual deletion;
  on a personal Mac an unsigned **binary** self-daemon (self-installing, self-healing)
  is meaningfully harder to remove casually than a single script — i.e. more friction
  where more friction is available.

The question this settles: should the browser layer be one codebase or several, and
where does the enforced/utility line fall now that the same browser engine can run
**supervised** or **standalone**?

## Decision

**There is now ONE browser codebase occupying THREE named positions, spanning BOTH
tiers:**

1. **Enforced browser plugin** — `browser-monitor` run by the platform. Signed,
   bundled, supervised; part of the **enforced tier**. Never self-installs or self-
   heals — the platform owns its lifecycle. *(Now bundled into the platform and enabled
   in the default schedule, so enforced browser blocking is live on a normal install —
   previously it was built but not shipped.)*
2. **Standalone browser self-daemon** — the **same** browser-monitor binary, launched
   so it installs itself as a user-mode background helper that heals-then-scans and
   survives casual deletion. **Utility/fallback tier.** For a **personal Mac with no
   enforced platform.** Removable, no signing, no tamper-resistance, no commitment-gate.
3. **Script fallback (mac-browser-guard)** — the single-file script (FEATURE 20).
   **Utility/fallback tier.** Kept for the **one environment a binary can't run at
   all**: the locked-down/app-allowlisting corporate Mac, where allowlisting blocks an
   unsigned binary but the OS-native scripting runtime is still permitted.

**Positions 1 and 2 are the same binary and share one scan engine and one blocklist** —
the tier depends on **how it is launched**, not on which artifact. **Position 3 is a
separate tiny script**, but its blocklist is **generated from the shared source of
truth**, so the three positions can no longer drift apart.

The enforced/utility line therefore moves: it is no longer "which browser codebase," it
is **"is the platform running it, or is it running itself."** Supervised = enforced;
self-installed = utility/fallback. The utility positions never claim the enforced tier's
properties (ADR-0021 still governs that).

## Alternatives considered

- **Keep the enforced plugin and the fallback as two separate codebases (status quo).**
  Rejected: two browser engines and two blocklists **drift** — the recurring honesty
  failure. Unifying the engine and generating the one blocklist from a single source
  removes the drift by construction.
- **Make the script the only fallback (no standalone binary).** Rejected: on a personal
  Mac the single-file script self-heals only against casual deletion; an unsigned-binary
  self-daemon gives **more casual-deletion resilience** where the environment allows a
  binary. The script stays the answer only where a binary genuinely can't run.
- **Retire the script now that a binary exists.** Rejected: the script owns the **one**
  case the binary can't cover — the allowlisting corporate Mac that blocks unsigned
  binaries but ships a scripting runtime. Dropping it would abandon exactly the
  environment ADR-0021 created the fallback tier for.
- **Have standalone-install probe for an already-present enforced platform and defer to
  it.** Rejected: probing would mean **enumerating the deliberately-hidden platform
  identifiers** — reintroducing the very tells the disguise work removed. Instead the two
  coexist **idempotently** (a double browser-quit is harmless), and standalone-install
  **advises** rather than probes.

## Consequences

- **Enforced browser blocking is now live on a normal install** (bundled + enabled by
  default), not a built-but-dormant plugin.
- **One blocklist, three positions** — a change to the blocked-site list propagates
  everywhere from a single source; a drift check keeps the generated copy honest.
- **The standalone self-daemon is a new utility-tier instance** with its own (thin)
  resilience story, distinct from both the enforced plugin and the script.
- **The script fallback's scope narrows to its true home** — the allowlisting corporate
  Mac — while the personal-Mac-without-platform case is now better served by the binary.
- **Coexistence is idempotent and blind** — no probing of the hidden enforced install;
  the layers simply don't fight (a redundant quit is harmless).
- **Verification** keeps the enforced plugin inside the platform mesh regression suite,
  and the two utility positions in the **utility-tier** section of the e2e history
  (different execution path — ADR-0021's separation holds; see e2e-test-history).

## Honest limitation

The standalone self-daemon is **still thin friction, not durability** — the same caveat
as the script (ADR-0021). It is user-mode, unsigned, has no tamper-resistance and no
commitment-gate; a determined user with a terminal removes it, and it self-heals only
against casual deletion. Being a binary rather than a script raises the casual-removal
bar slightly; it does **not** make it enforced, and it is deliberately outside the
threat model. Only the enforced plugin (platform-supervised) carries the enforced tier's
guarantees. Treating either fallback as more than a courtesy for a machine the platform
doesn't cover would misrepresent what focusd protects.

## References
- Amends: `0021-coverage-tiers-enforced-vs-utility-fallback.md`
- Script fallback feature: `../features/20-mac-browser-guard.md`
- Enforced browser path: the `browser-monitor` plugin
- Register tier note: `../REQUIREMENTS_REGISTER.md` §5 (Layered enforcement)
- Friction-not-cryptography principle: register §5
- PR branch: `feat/browser-standalone`
