# Feature 20 — mac-browser-guard (user-mode fallback for locked-down Macs)

- **Status:** ✅ shipped (PR #85; live-verified on a real Mac)
- **Tier:** **utility / degraded fallback** — NOT the maintained enforcement path
- **Location:** `utils/mac-browser-guard/` (`browser_guard.py` + README)

## What

A standalone, single-file macOS utility that **quits any browser sitting on a
blocklisted site**. It watches the open browsers (Chrome / Brave / Edge / Safari),
reads which sites their tabs are on — including tabs that aren't the active one —
and if any tab is on a blocklisted site (google / youtube / bilibili / steam /
dota / news / etc.), it tells that browser to quit. The blocklist is a plain,
user-editable list.

It runs **entirely in user mode**: no admin rights, no sudo, no installer, no
system changes. It uses only automation facilities macOS already ships with, so
it needs nothing that a locked-down machine would forbid installing.

Two commands beyond a one-off scan:
- **`on`** — self-installs so the guard keeps running in the background and
  **self-heals** if pieces of it are deleted.
- **`off`** — removes it.

## Why it exists / positioning

This is a **cheap user-mode fallback for the one environment where the full
focusd stack cannot run**: a **locked-down / managed corporate Mac** where the
user has **no admin rights** and application-allowlisting controls (ThreatLocker-
style) block installing the daemon/platform — and also block a Freedom-style app.
There, the whole multi-layer protection is simply impossible.

In that corner, this one script buys **partial coverage (browser distraction
only)** using tools already approved on the machine. That is its entire reason to
exist: some friction where otherwise there would be none.

It is explicitly a **utility, not an enforced layer.** On any machine where focusd
proper can run, the **browser-monitor plugin** is the real, maintained enforcement
path (signed, driven by the platform). This script is the degraded stand-in for
where that can't be installed.

## How it behaves (product rules)

- **Quit on a blocklisted tab.** If any open tab in any watched browser is on a
  blocklisted site, that browser is quit. Non-active background tabs count too.
- **User-editable blocklist.** The set of blocked sites is a plain list the user
  edits; matching covers a site and its subdomains.
- **Self-heal against casual deletion.** Once turned `on`, if pieces of the guard
  are deleted it restores itself and keeps running — casual removal doesn't stick.
- **One clean off switch.** `off` removes the guard deliberately.
- **Asks once for permission.** The first run prompts (once) for the macOS
  automation permission it needs to read tabs and quit browsers — no sudo.

## Acceptance criteria (testable behaviour)

1. **Detect + quit.** With a watched browser open on a blocklisted site (active
   *or* background tab), the guard quits that browser. *(Live-verified: it saw all
   open tabs, including non-active ones, and quit the browser.)*
2. **User-mode only.** Runs and installs with no admin rights / no sudo / no
   system-level changes.
3. **Self-heal.** After `on`, deleting a piece of the guard does not stop it —
   it restores and continues. `off` removes it cleanly.

## Honest limitations

- **Browser tabs only.** No app-killing, no network / DNS / packet blocking —
  none of the platform's layers. It addresses browser distraction and nothing
  else.
- **User-mode and removable.** It self-heals against casual deletion, but a
  determined user with terminal access can just run `off`. There is **no signing,
  no tamper-resistance, and no commitment-gate / uninstall-ritual** like the
  daemon — none of focusd's durability. It is friction only, and thin friction.
- **Depends on the macOS automation permission being granted;** a best-effort
  fallback schedule may additionally need Full Disk Access (the primary
  background path does not).
- **Not for machines where script execution itself is policy-controlled.** There
  it should go through the proper approval channel — this is a fallback for where
  the stack *can't* be installed, not a tool for routing around controls that
  forbid running scripts.
- **macOS only.**
