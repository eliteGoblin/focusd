# Feature 09 — `daemon status` health snapshot

- **Status:** in build (2026-06-01) · ships with the next daemon release
- **Decision:** [ADR-0011](../decisions/0011-status-redaction.md) (redaction) ·
  [ADR-0012](../decisions/0012-status-delegates-to-platform.md) (KISS layering)

## What

A single read-only command — `daemon status` — that prints a readable health
snapshot of the focusd install and ends with one overall verdict.

The command is split along a deliberate seam (see ADR-0012):

- **`daemon status` reports only daemon-owned facts:** is the protection
  engine running (how many of the launchd mesh roles are up), is the platform
  process alive, and the platform version (desired vs last-known-good). The
  daemon does **not** know a plugin exists and does **not** read the platform's
  state — it stays plugin-agnostic by design.
- **`platform status` owns the protection detail:** per-protection last result
  + how recently, blocklist size, packet-filter table size, skill files
  present. `daemon status` **delegates** to it and passes its output through,
  so the operator still sees one combined snapshot.

It answers the calm operator's honest question — *"is my commitment device
actually working right now?"* — without ever handing the weak-moment self the
strings they would need to tear it down.

## Why

Today the only way to know if focusd is healthy is to run discovery commands
that enumerate the very identifiers the design hides (the disguised workdir,
the launchd labels, the daemon binary name, the pf anchor). That is exactly
the leak the threat model warns about: an *indirect* question ("is it
running?") whose answer is a *direct* bypass recipe.

`daemon status` closes that gap. It gives the operator a trustworthy health
read whose output is, by construction, safe to show — even on a screen the
weak-moment self is looking at, even when something is broken.

## How it behaves (product rules)

- **One readable snapshot, two owners.** The daemon contributes: engine
  running (mesh roles up) · platform process alive · platform version (desired
  vs last-known-good). The platform contributes (passed through): each
  protection's last result + how recently · blocklist size · pf table size ·
  skill files present. One overall verdict closes it.
- **Three verdicts.** `HEALTHY` (everything that should be working is) ·
  `DEGRADED` (partial — a fresh install still warming up, a stale protection,
  version drift, or admin-level protections unavailable under a user install) ·
  `DOWN` (the engine isn't running, the host blocklist is gone, or all skill
  files are missing).
- **Mode-aware, honestly.** Under a **user (limited) install**, the
  admin-level protections (site/game/packet) report **UNAVAILABLE — needs
  admin install**, never "failed" or "down". Overall reads `DEGRADED` with a
  hint to reinstall with admin rights for full coverage.
- **Degrades gracefully without admin.** Querying a full (admin) install
  without admin rights can't see everything; those lines read
  **"unknown (re-run with sudo)"** rather than hard-failing the command.
- **Fresh install is healthy, not broken.** An install younger than ~10
  minutes with no protection runs yet reads `HEALTHY — warming up`, not
  `DEGRADED`.
- **Machine-readable too.** `--json` emits the same snapshot for scripts.
  Colour is honoured off a TTY and suppressed by `--no-color` / `NO_COLOR`.

## The redaction contract (load-bearing)

The output — text **and** json — **never** contains the disguised
identifiers: the workdir path, the launchd labels, the daemon binary
filename, or the pf anchor name. This holds **structurally, on every path,
including every error path** — not as a best-effort scrub. See
[ADR-0011](../decisions/0011-status-redaction.md) for why this is built into
the type system rather than left to the renderer's discipline.

Where a disguised value would otherwise appear, the output shows
`<redacted>`. The command exposes counts, ages, versions, and verdicts —
never the strings a teardown needs.

## Acceptance criteria (testable behaviour)

1. On a healthy install, `daemon status` reports the engine running and an
   overall `HEALTHY` verdict, and exits `0`.
2. `daemon status` reports daemon-owned facts (mesh roles up, platform process
   alive, platform version) and passes through the platform's protection
   detail; the operator sees one combined snapshot.
3. The status output **never contains a filesystem path, a launchd label, a
   daemon binary filename, or a pf anchor** — in text or json, on every path
   including error/broken-install paths. Enforced by a snapshot test fed
   deliberately poisonous values.
4. A user can read status of a **system install without sudo**: admin-only
   facts (mesh role count, admin-level protections) degrade to **"unknown
   (re-run with sudo)"** rather than the command failing.
5. Under a user-only install, admin-level protections read **UNAVAILABLE
   (needs admin install)** — not down, not failed — and the overall verdict is
   `DEGRADED`.
6. Exit codes: `0` healthy · `1` degraded · `2` down · `3` internal error.
7. A fresh install (<10m, no runs recorded) reads `HEALTHY — warming up`.
8. The daemon never reads the platform's state directly: it gets protection
   detail only by delegating to the platform's own status. (Verifiable as a
   product behaviour: with the platform process down, the daemon still reports
   its own facts and marks platform detail unavailable, rather than erroring.)

## Honest limitations

- Status is a **read** of observed state; it is not itself a protection. A
  green status doesn't prove a determined bypass is impossible — only that the
  layers that should be up, are up. It is calibration, not enforcement.
- Per-protection recency is a **proxy via last-run status** reported by the
  platform, not a live re-probe of each protection at status time. It tells
  you when a protection last acted and how that went — slightly less immediate
  than re-running every check on the spot. This is the deliberate cost of
  keeping the daemon plugin-agnostic (ADR-0012).
- On a system install **without sudo**, the mesh and admin-level facts are
  genuinely unknown to the command and read **"unknown"** rather than guessing
  or hard-failing. Run with sudo for the full read.
- Recency/age buckets are coarse (`<1m` / `<5m` / `<1h` / `>1h`) on purpose —
  precise timestamps add no operator value and risk fingerprinting.

## Non-goals

- No flag (`--show-paths`, `--debug`, `--verbose-internal`) that re-exposes
  the disguised identifiers. There is deliberately no escape hatch — that
  would reopen the exact leak this feature closes.
- Not a control surface. `status` never stops, starts, kills, or mutates
  anything; it only reads.
