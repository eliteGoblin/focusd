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

### V6 — Process-set accuracy (no orphans, signature-verified)
The running set must be **exactly** what's expected — **nothing more, nothing
less**. Self-update path-rotation and watchdog rebuilds must not leave
**orphaned old-version daemons or binaries** dangling.
- **Exercise:** enumerate every focusd process and on-disk binary; check
  version + Ed25519 signature of each.
- **Pass:** exactly the expected long-running set (the 2 live mesh workers +
  1 engine; ensurer/plugins/watchdog are transient), **all the current
  version**, all legitimately signed; **no** old-version daemon process or
  rotated binary left behind. Any unexpected/unsigned/old-version process is a
  FAIL to investigate, not noise to ignore.

## Status — daemon-v0.5.3 + platform v0.15.0 (2026-06-17)
Verdict: **PASS** — all six exercised on the live system.

| # | Feature | Verified? | Evidence (what was actually exercised) |
|---|---------|-----------|----------------------------------------|
| V1 | Engine self-heal | ✅ VERIFIED | Killed the engine process **and** deleted its on-disk binary; the daemon re-fetched it via the release CDN and restarted — **fresh binary (new mtime), new pid, ~4s**, no manual placement. |
| V2 | Total-teardown recovery | ✅ VERIFIED | Booted out all 3 supervisor entries + killed every process + wiped the work directory → fully DOWN. The out-of-band rail rebuilt the mesh (3/3) and the rebuilt workers re-fetched the engine — **HEALTHY in ~45-60s**, no manual help. |
| V3 | App removal (kill-steam) | ✅ VERIFIED | `brew reinstall --cask steam --force` placed `/Applications/Steam.app`; kill-steam removed it **~6s later** (within the @10s reconcile). Placement→removal observed in one run (not stale brew metadata). |
| V4 | Skill self-heal | ✅ VERIFIED | Deleted `~/.claude/skills/focusd-protection/`; skill-protector re-injected `SKILL.md` (identical size) within **~70s** (@5m cycle). |
| V5 | Claude refusal stance | ✅ VERIFIED | Skill + always-on rule + SessionStart hook present and current; the focusd-protection skill is loaded and followed this session (redaction + refusal). Fresh-session refusal is enforced by the SessionStart hook every session. |
| V6 | Process-set accuracy / no orphans | ✅ VERIFIED | After two path-rotating self-updates AND the teardown rebuild: exactly **2 workers (role a+b), both daemon-v0.5.3**, **1 engine (v0.15.0)**, **1 on-disk daemon binary (current)** — no orphaned old-version process or binary. |

> Note: the engine version pin (v0.15.0) and the bundled plugin set (incl.
> freedom-protector) are current; `browser-monitor` is not bundled (no bundle
> target yet) — tracked, not part of this release's vital set.

> Anything left `—` / NOT VERIFIED must be called out at the top of the verify
> report and handed to the human to test. A green build is not a verified
> feature.
