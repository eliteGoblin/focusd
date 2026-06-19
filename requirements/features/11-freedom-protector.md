# Feature 11 — freedom-protector plugin (keep the Freedom app alive)

- **Status:** ✅ shipped (PR #51)
- **Layer:** protection plugin (platform job, runs as the current user)

## What

A protection plugin that keeps the third-party **Freedom** focus app
(Freedom.to) — and its background helper, **FreedomProxy** — running. On a
schedule the plugin checks whether each is alive and **relaunches whichever
has been shut down**, so quitting Freedom doesn't actually free the user: it
comes back on its own within seconds.

This is the inverse of the kill-steam plugin. Where kill-steam *removes* an
app the user shouldn't have running, freedom-protector *defends* an app the
user committed to keeping on. Freedom does the actual website/app blocking;
focusd's job here is simply to make sure the user can't disable that blocking
by quitting Freedom.

## Why

Freedom is a real blocking tool, but its protection is only as durable as the
app staying open — and quitting an app is the easiest possible weak-moment
bypass (no terminal, no sudo, just Cmd-Q). That undoes the user's commitment
in one click. freedom-protector closes that gap by treating "Freedom is
running" as desired state and continuously reconciling toward it, the same
keep-it-true model the rest of focusd uses.

## How it behaves (product rules)

- **Relaunch what's down, leave what's up.** Each pass relaunches only the
  Freedom app and/or the FreedomProxy helper that are currently not running.
  If both are already up, the pass does nothing — it's idempotent.
- **Fast cadence.** Runs frequently (~10s) so a quit Freedom is back almost
  immediately, not minutes later.
- **Never hangs.** Every relaunch attempt is time-bounded, so a stuck launch
  can't stall the protection loop.
- **Clean skip when Freedom isn't installed.** If Freedom isn't present on the
  machine, the plugin does nothing and reports a benign skip — it is not an
  error to run on a machine without Freedom.
- **A failed relaunch is recorded, not fatal.** If one target can't be
  relaunched this pass, the plugin records it and continues; the next pass
  tries again.

## Acceptance criteria (testable behaviour)

1. **Relaunch on quit.** With Freedom installed and either the app or the
   proxy not running, the next pass relaunches the missing target(s). With
   both already running, the pass relaunches nothing (idempotent).
2. **Well-behaved job.** A relaunch that hangs does not stall the pass (it
   returns promptly under its time bound); a process-enumeration error fails
   cleanly; a single relaunch failure is recorded without aborting the pass.
3. **Benign skip.** On a machine where Freedom isn't installed, the plugin
   skips cleanly rather than erroring.

## Honest limitations

- **Login-item re-enable is best-effort, not guaranteed.** Freedom's "start at
  login / allow in the background" setting lives in a macOS surface that has
  **no reliable public way to flip back on** from outside the app. The plugin
  does **not** claim to machine-verify a re-enable; relaunching the app is the
  only legitimate nudge (it lets Freedom re-register itself), and this state is
  recorded as **manual-verify**. Mirrors the FEATURE 10 Login-Items honesty.
- **Keeps it running; doesn't stop a determined user.** A user who fully quits
  *and* prevents relaunch (e.g. removing/renaming the app), or who disables
  Freedom's own blocking from inside Freedom, is outside this plugin's reach.
  It defends against the easy quit, not every teardown.
- **macOS only.** Freedom targeting is macOS-specific today.
- **Depends on Freedom being installed and configured** by the user; focusd
  keeps it alive but does not install or license it.
