# Vital-feature verification contract

> **Self-recovery is non-negotiable** (see register §5). "Verified" means the
> **real failure → recover path was exercised and the system healed itself** —
> never a surface "it's up" check, never a state propped up by a hand-placed
> artifact. Every release MUST exercise the features below and the verify
> report MUST state, per feature, **VERIFIED** or **NOT VERIFIED** with the
> actual evidence. No guessing. If it wasn't exercised, it is NOT verified —
> say so and hand it to the human to test.

## Why this doc exists
A "healthy" status masked a broken self-heal for a long time: the protection
engine binary had been hand-placed, so the daemon *looked* healthy while it
could not actually re-fetch the engine on its own (ADR-0017). The lesson:
**verify the recovery, not the steady state.** This contract lists the vital
features and the failure each must self-heal from.

## The vital features (must be verified every release)

### V1 — Protection-engine self-heal
The engine (and its plugins) must come back **without manual intervention**
after it is taken down or its on-disk binary is removed.
- **Exercise:** kill the engine process **and** delete its on-disk binary (so a
  restart cannot reuse a deleted inode). Optionally wipe the work directory.
- **Pass:** within the heal window the engine is running again **from a freshly
  re-fetched binary** (fresh mtime / new pid), status HEALTHY — with no human
  placing the binary.

### V2 — Total-teardown recovery (out-of-band)
A total atomic teardown (all mesh entries + all processes + the work directory,
at once) must still recover.
- **Exercise:** remove all mesh entries, kill all processes, wipe the workdir.
- **Pass:** the out-of-band watchdog rebuilds the mesh and the engine is
  re-fetched and running; status HEALTHY. (Recovery window is coarse — minutes —
  by design.)

### V3 — App removal (kill-steam)
A blocklisted app that launches must be removed.
- **Exercise:** install/launch Steam (e.g. `brew install --cask steam`, or open
  `Steam.app`) and/or Dota 2.
- **Pass:** the plugin removes the running process / on-disk app within its
  schedule; confirm it is gone.

### V4 — Claude-skill self-heal (skill-protector)
Deleting the Claude refusal skill (or its rule / SessionStart hook) must
auto-recover.
- **Exercise:** delete `~/.claude/skills/focusd-protection/` (and/or the
  always-on rule / the settings hook).
- **Pass:** the plugin re-injects the files within its schedule; confirm they
  are back and current.

### V5 — Claude refusal stance
Asking Claude to stop / disable / uninstall focusd must be **refused**; the
5-gate override ritual is the only path.
- **Exercise:** confirm the focusd-protection skill + always-on rule +
  SessionStart hook are present and current; a disable request is refused.
- **Pass:** refusal holds; no bypass without all override gates.

## Per-release status table (fill HONESTLY)
| # | Feature | Verified? | Evidence (what was actually exercised) |
|---|---------|-----------|----------------------------------------|
| V1 | Engine self-heal | — | |
| V2 | Total-teardown recovery | — | |
| V3 | App removal (kill-steam) | — | |
| V4 | Skill self-heal | — | |
| V5 | Claude refusal stance | — | |

> Anything left `—` / NOT VERIFIED must be called out at the top of the verify
> report and handed to the human to test. A green build is not a verified
> feature.
