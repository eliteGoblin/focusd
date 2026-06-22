# Feature 15 — Keep the enforcement plugins authentic, every tick, not just at startup

- **Status:** ✅ shipped (platform v0.16.0 + v0.16.1, 2026-06-22 — built, deployed live, e2e-verified)
- **Decision:** [ADR-0019](../decisions/0019-embedded-bundle-is-plugin-trust-root.md)
- **Builds on:** Ed25519 signed releases (register §4) — the signed platform
  binary is the trust root this feature leans on · the reconcile loop /
  signed-desired-state spine (the platform's "tighten-only" enforcement model)
- **Class of defect it closes:** the recurring **false-green / latent-failure**
  pattern (register §5 self-recovery principle, §6) — a protection that is dead
  while status reads "ok."

## What

The platform enforces blocking by running a set of small plugin programs on a
schedule (process-killing, host/packet blocking, app-defending, skill
re-injection, etc.). Today the genuine plugin programs are unpacked to disk
**once, at platform startup**, and from then on the schedule simply runs
**whatever program is sitting at that location** — it never re-checks that the
program is still the real one. The schedule trusts the disk.

That is a hole. Anyone who can write to disk — and on this machine the
weak-moment owner is root — can quietly swap a plugin program for a do-nothing
stand-in. The schedule keeps "running the plugin" on time, but the plugin now
does nothing. The protection is dead, and **it stays dead until the next platform
restart**, which may be a long time. Worse, if the stand-in simply exits cleanly,
`status` reports that plugin as **"ok"** — a **false green over dead
protection**. The product owner demonstrated this live: replacing the
Steam-killer with a no-op silently turned off Steam/Dota enforcement while status
still looked healthy.

This feature makes each plugin program **part of the continuously-enforced signed
desired state**, exactly like the blocking policy itself. The platform already
carries a known-genuine copy of every plugin inside its own signed, verified
binary. On **every** reconcile pass (and again right before a plugin is run), the
platform checks the on-disk plugin against that genuine copy. If they don't
match, it **restores the genuine program and runs that** — and it records the
mismatch as a **tamper event**, so it can never again be read as healthy.

## Why

focusd's adversary is the user's own weak-moment self, who on this machine has
root. "Unpack once, then trust the disk forever" hands that self the single
cheapest, quietest bypass of the **entire** enforcement layer: overwrite one
small file and the corresponding protection goes away with no alarm, no
self-heal, and a green status light on top of it. Of every weakness found so far
this is the most severe, because it is a **direct enforcement bypass** of the
whole plugin layer rather than a leak or a head-start, and because it trips the
project's most dangerous failure shape — **a protection that is gone while the
system reports it present** (register §5, §6).

The fix needs **no new keys and no new trust infrastructure**. The genuine plugin
programs already travel *inside* the platform binary, which is already
Ed25519-signed and already verified before it is allowed to run. So the genuine
embedded copy is **already the trusted golden reference**. Authenticity is simply:
does the program on disk match the genuine embedded copy? Promoting plugin
programs from "placed once" to "reconciled every tick" is the same self-recovery
discipline the rest of the system already lives by (§5: "recovered must mean the
system re-created what it needed on its own"). It also closes the false-green
class directly: a tampered plugin becomes a **recorded security event**, not a
silent gap.

## How it behaves (product rules)

- **Authentic-or-restored, every tick.** On every reconcile pass, each plugin
  program on disk is checked against the genuine copy the platform carries. A
  match runs as-is; a mismatch is restored to the genuine version and then run.
- **Checked again at point of use.** A plugin is also confirmed genuine
  immediately before it is run, not only on the loop's schedule — so a swap that
  lands between checks is still caught before the stale program can execute. (The
  exact balance here is a design question, below.)
- **Restore is safe even mid-run.** Restoring a plugin's program replaces it as a
  single, all-or-nothing swap, so a plugin that happens to be running while it is
  being restored is never left half-written or corrupt.
- **Tamper is an event, not a silent repair.** A detected mismatch is recorded as
  a **security event (tamper detected → repaired)** — written to the app log
  (FEATURE 16) and recorded as a platform event in the state DB. That audit
  record is where the "was there a tamper attempt?" question is answered, never
  silently lost.
- **`status` reflects CURRENT state only (KISS).** `status` is a current-health
  read, not a history view. A plugin's verdict = is it the genuine binary right
  now AND did its last run succeed? Genuine + working → **ok**. A tamper that has
  **not** been restored, or a real run error → **not-ok**. A disabled plugin →
  **disabled** (excluded from OVERALL). A tamper that has since been auto-restored
  reads **ok**, because the plugin IS genuine and enforcing again — that is the
  honest current state. *(This refines the original F15 design, which kept a
  persistent "tampered → repaired Nx" verdict in status for 24h; that conflated
  audit history into current health. Owner intent, verbatim: "status means current
  state, it has to be clean; I don't want to see any tampered message if not
  tampered; the history can go into the log/events — don't mix things up.")*
- **Why a recovered plugin reading `ok` is still honest.** The original
  false-green (a no-op plugin reading ok while protection was dead) is prevented
  by the **restore mechanism** above — point-of-use verify restores the genuine
  binary before it runs — **not** by a persistent status flag. A
  currently-broken / not-restored plugin still reads not-ok, and a sweep that is
  failing **right now** is a current problem that still surfaces. So nothing is
  weakened: status stays truthful about the present, and the tamper history lives
  in the audit channel.
- **No behaviour change when nothing is wrong.** On an untampered system the
  plugins run exactly as before; this feature is invisible until something on
  disk doesn't match the genuine copy.
- **A deliberately-off plugin is not a failure.** An intentionally-disabled
  plugin (e.g. network-block, off by default) must **not** drag the overall
  health to DEGRADED. "Degraded" must mean an *enabled* protection actually
  failed — not that an opted-out one isn't running. (Closes the false-DEGRADED
  truthfulness gap alongside the false-green one.)

## Acceptance criteria (observable behaviour)

1. **Self-heal within one tick.** A plugin program overwritten on disk with a
   different program (e.g. a no-op stand-in) is detected and **restored to the
   genuine version within one reconcile tick**, and the genuine program — not the
   substitute — is what actually runs.
2. **No false green, ever — via restore, not a status flag.** A substitute that
   exits cleanly can no longer buy a green light over dead protection, because the
   genuine binary is restored before it runs. While a tamper is **unrestored**,
   status reads not-ok. Once auto-restored, the plugin is genuine and enforcing,
   so status truthfully reads **ok** — and the tamper-and-repair is recorded in
   the audit channel (log + event, criterion 4), not held as a persistent status
   verdict.
3. **Point-of-use safety.** A plugin swapped in **between** reconcile passes is
   still caught and restored before the stale/substitute program is run — the
   substitute never executes in place of the genuine one.
4. **Tamper is recorded.** Each detected mismatch produces a durable,
   observable record (a security event), consistent with the observability
   principle (register §5) — a tamper is never repaired silently with no trace.
5. **No regression when clean.** On an untampered system, every plugin runs on
   its normal schedule with its normal behaviour and no spurious tamper events.
6. **No false degraded.** An intentionally-disabled plugin does **not** drive
   overall health to DEGRADED. With every *enabled* protection healthy, overall
   reads healthy even though an opted-out plugin isn't running.

## Honest limitations

- **Friction + fast self-heal + detection — not an unbreakable seal.** Root can
  re-tamper immediately, or race the restore in a tight loop. This feature does
  **not** stop a determined, scripted root attacker; it makes an *impulsive* swap
  fail fast (restored within ≤1 tick) and, crucially, **loud** (recorded, no
  false green). That is consistent with the project's friction-grade philosophy
  (register §5) — the durable weight remains the server-side override gate.
- **Binary integrity only — not config/policy integrity.** This covers the
  authenticity of the plugin **programs**. It does **not** cover the integrity of
  what each plugin is *told to do* — blocklists, target-app lists, job schedule,
  and the "no inside door handle" tighten-only guarantee for plugin **config**.
  A genuine plugin program pointed at a gutted blocklist is a *separate* hole,
  tracked as a follow-up (see icebox + design questions). This feature must not
  be described as protecting plugin policy.
- **A restart-window assumption is removed, not a root barrier added.** The value
  delivered is precise: it removes the "dead until restart" window and the
  false-green over a swapped binary. It does not change what root can ultimately
  do to the machine.

## Design questions / cross-cutting notes (for the architect — do not pre-decide)

- **Point-of-use verify vs loop-only (the timing window).** Verifying only on the
  loop leaves a small window between a swap and the next check; verifying again
  right before each run closes most of it but not the instant between the check
  and the exec. The architect should decide how tight this needs to be and where
  to spend the cost — name the residual window honestly rather than implying it's
  zero.
- **The bigger structural option: remove the on-disk attack surface entirely.**
  Rather than continuously repairing on-disk plugin programs, the plugins could
  be **carried and run from inside the signed platform binary itself**, so there
  is no separate on-disk program to overwrite in the first place. That is a
  larger architectural change with its own trade-offs (build shape, per-plugin
  isolation, cross-platform packaging). Flag it as the strategic alternative to
  weigh against the reconcile-and-repair approach; this feature does not commit
  to it.
- **3-platform Go still holds.** The integrity check + atomic restore should stay
  OS-agnostic where it can; any OS-specific bits live behind the existing per-OS
  adapter seam (register §5). The daemon stays thin — this is platform-layer
  behaviour; the daemon knows nothing about individual plugins.
- **Where the tamper event surfaces.** Confirm in DESIGN that the tamper record
  flows into the same status/observability path that already exists, so it can't
  be read as false-green and so the observability principle (§5) is honoured.
