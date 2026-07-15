# FEATURE 27 — browser-monitor standalone (runs under the platform AND self-runs when it isn't)

- **Status:** 🟡 **DEFINING** (2026-07-15) — captured, **not yet scheduled**.
  **⚠️ Needs a human decision** on its relationship to
  [FEATURE 20 (mac-browser-guard)](20-mac-browser-guard.md) and the enforced-vs-utility
  tier split ([ADR-0021](../decisions/0021-coverage-tiers-enforced-vs-utility-fallback.md)).
- **Tier:** **enforced** when it runs under the platform · **utility / best-effort**
  when it self-runs standalone.
- **Reference implementation:** the shipped user-mode browser guard (FEATURE 20,
  `utils/mac-browser-guard/browser_guard.py`) already proves the standalone,
  no-sudo, automation-based approach on a real locked-down Mac.

## Why

The browser-monitor plugin delivers real value — it **quits a browser sitting on a
blocklisted tab** — but today it **only runs when the full focusd platform is
installed.** On a **locked-down / managed corporate Mac** (no admin rights,
app-allowlisting like ThreatLocker blocks installing the daemon/platform), the platform
can't run, so the plugin never delivers there. That corner is exactly where FEATURE 20
already showed a standalone user-mode guard works.

This feature folds that capability into the browser-monitor itself: **one thing** that
runs as the signed enforced plugin under the platform **and** can **self-run standalone**
where the platform isn't installed — so the corp-machine user gets browser-distraction
coverage without needing a separate tool.

## What

- **Under the platform — unchanged.** The browser-monitor runs as the signed,
  platform-driven **enforced** plugin (the real, maintained enforcement path).
- **Standalone (best-effort daemon mode).** When the platform isn't installed, the
  browser-monitor **self-runs**: it self-installs a **user-level background runner**
  (user launchd + a **cron fallback**) and **periodically self-restarts / heals**, so
  casual deletion doesn't stop it.
- **User-mode only.** Automation-based (osascript), **no sudo / admin / system
  changes** — so it runs where the locked-down machine forbids installing the stack.
- **Same job either way.** Quit any watched browser (Chrome / Brave / Edge / Safari)
  sitting on a blocklisted tab — including non-active background tabs — against a
  plain, user-editable blocklist.

## How it behaves (product rules)

- **Platform present → enforced plugin** (platform-driven, signed). **Platform absent
  → best-effort user-mode daemon** that keeps itself alive.
- **Self-heals against casual deletion** in standalone mode; a **clean off switch**
  removes it deliberately.
- **Asks once** for the macOS automation permission it needs — no sudo.

## Acceptance criteria (testable behaviour)

1. **Enforced under the platform.** With the platform installed, the browser-monitor
   runs as the enforced plugin — behaviour unchanged.
2. **Self-runs standalone.** With the platform **not** installed, it self-installs a
   user-level runner, **keeps running across logout/restart** (user launchd + cron
   fallback), and **self-restarts / heals** after casual deletion.
3. **User-mode only.** Installs and runs with **no** admin / sudo / system-level change.
4. **Delivers coverage where the stack can't run.** On a locked-down Mac, it still
   quits a browser on a blocklisted tab.

## Honest limitations

- **Utility / best-effort tier when standalone (ADR-0021).** User-mode and **removable**:
  it self-heals against casual deletion, but a determined user with a terminal just
  removes it. **No signing, no tamper-resistance, no commitment-gate / uninstall
  ritual** — thin friction, not durability. (Under the platform, the enforced tier's
  guarantees apply.)
- **Browser tabs only.** No app-kill, no network / DNS / packet blocking — none of the
  platform's other layers.
- **Depends on the macOS automation permission;** the cron-fallback path may
  additionally need Full Disk Access (the primary user-launchd path does not).
- **macOS only.** Not for machines where running scripts is itself policy-controlled —
  use the proper approval channel there, not this.

## Open design questions (for the human gate)

- **Supersede or coexist with FEATURE 20?** Does the standalone browser-monitor
  **absorb** the standalone Python guard (F20 becomes its standalone mode), or do the
  two **coexist** (F20 stays the throwaway script; F27 is the productized plugin mode)?
- **ADR-0021 update.** Wherever this lands, ADR-0021's enforced-vs-utility tier split
  should record where the **standalone** browser-monitor sits — it is the first thing
  that spans both tiers.
- **Persistence framing.** Standalone self-heal is **user-mode best-effort** (the same
  shape FEATURE 20 already ships), **not** the enforced-daemon "survive a full delete"
  pattern that FEATURE 22 / HF2 gates behind the malware-persistence approval path —
  keep that distinction explicit so the two aren't conflated.
