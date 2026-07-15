# ADR-0021 — Two coverage tiers: the enforced platform vs a utility/fallback tier

- **Status:** accepted (2026-07-02) · shipped · **AMENDED in part by [ADR-0022](0022-one-browser-codebase-both-tiers.md) on 2026-07-15.** The two-tier model (enforced vs utility/fallback) **stands**; what changes is that the fallback tier now has **two** instances and the **same** browser codebase spans **both** tiers depending on how it is launched — so the "Fold it into the `browser-monitor` plugin — Rejected" alternative below is reversed in part (the codebase was unified; the execution paths stay distinct). See ADR-0022.
- **Feature:** [FEATURE 20](../features/20-mac-browser-guard.md) (mac-browser-guard — first instance of the fallback tier)
- **Decided by:** Frank (product owner), BA review
- **Relates to:** register §5 "Layered enforcement" (enforced tier vs utility/fallback
  tier note) · the `browser-monitor` plugin (the maintained browser enforcement) ·
  PR #85.

## Context

focusd's whole design is an **enforced** stack: a self-protecting daemon + platform
+ plugins, signed, tamper-resistant, self-healing, needing admin/root. That is the
maintained real protection and the only thing the threat model (register §3) speaks
to.

But there is one environment where **none** of it can run: a **locked-down / managed
corporate Mac** where the user has **no admin rights** and **application-allowlisting**
controls (ThreatLocker-style) block installing the daemon/platform — *and* also block
a Freedom-style app. The full stack is simply impossible there. The forces:

- The user genuinely wants some friction on that machine too (it is where a lot of the
  distraction actually happens).
- Nothing that requires an install, admin, or a system change is permitted.
- Some coverage beats none — even browser-only, even removable.

The open question this settles: does a cheap, user-mode, removable helper *belong* in
focusd at all, given focusd's identity is durable, tamper-resistant enforcement? If we
add it without a clear frame, it risks being mistaken for a real protection layer and
quietly eroding the "friction that actually holds" promise.

## Decision

**Recognise two distinct coverage tiers, and label every piece of focusd as one or the
other:**

- **(a) The enforced tier** — daemon + platform + plugins. Signed, tamper-resistant,
  self-protecting, needs admin/root. This is the maintained real protection and the
  subject of the threat model. Unchanged by this ADR.
- **(b) A utility / fallback tier** — user-mode helpers that give **cheap partial
  coverage** where the enforced tier **cannot run**. First instance:
  **mac-browser-guard** (FEATURE 20), a standalone user-mode script for the locked-down
  Mac. Explicitly outside the enforcement guarantees.

The tier is a **first-class label**: a feature is either enforced or utility, and the
utility tier never claims the enforced tier's properties. On any machine where focusd
proper can run, the enforced tier is the answer; the fallback tier is only for the
corner where it cannot.

## Alternatives considered

- **Don't ship it — enforced tier or nothing.** Rejected: leaves the locked-down
  corporate Mac with zero friction, the one place the user is most exposed. Some
  coverage beats none.
- **Ship it but present it as "another layer."** Rejected: it has none of the enforced
  tier's durability (no signing, no tamper-resistance, no commitment-gate). Calling it a
  layer would be a false-green at the *product* level — the exact honesty failure the
  register warns against. It must be labelled degraded.
- **Fold it into the `browser-monitor` plugin.** Rejected: that plugin *is* the enforced
  browser path and needs the platform to run. The fallback exists precisely because the
  platform can't be installed there; they are different tiers with different execution
  paths.

## Consequences

- The fallback tier is **browser-only, user-mode, removable** (no tamper-resistance, no
  commitment-gate / uninstall-ritual) and **explicitly degraded**. It does **NOT**
  replace the enforced tier.
- The **`browser-monitor` plugin remains the real, maintained browser enforcement** —
  the fallback is a stand-in only where the platform can't run, never a substitute where
  it can.
- Utility-tier items live and are verified **outside** the enforced platform's mesh
  regression suite (different execution path). The e2e history tags them distinctly so a
  utility TC is never read as a mesh/platform guarantee.
- Future user-mode fallbacks (other apps, other locked-down environments) now have a
  named home and a clear contract, instead of blurring into the enforced stack.

## Honest limitation

The fallback tier is **thin friction, not durability.** A determined user with a
terminal just removes it; it self-heals only against casual deletion. It addresses
browser distraction and nothing else — no app-kill, no network/DNS/packet blocking.
It is deliberately not covered by focusd's threat model. Treating it as more than a
courtesy layer for an impossible environment would misrepresent what focusd protects.

## References
- Feature: `../features/20-mac-browser-guard.md`
- Register tier note: `../REQUIREMENTS_REGISTER.md` §5 (Layered enforcement)
- Maintained browser enforcement: the `browser-monitor` plugin
- Friction-not-cryptography principle: register §5
