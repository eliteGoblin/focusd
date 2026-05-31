# Feature 09 — `daemon status` health snapshot

- **Status:** in build (2026-06-01) · ships with the next daemon release
- **Decision:** [ADR-0011](../decisions/0011-status-redaction.md)

## What

A single read-only command — `daemon status` — that prints a readable health
snapshot of the whole focusd install: is the protection engine running, what
version is live, when did each protection last act and how did it go, how big
is the blocklist, are the Claude-skill files present, and one overall verdict.

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

- **One readable snapshot.** Engine running? · platform version (desired vs
  live) · each protection's last result + how recently · blocklist size · pf
  table size · skill files present · an overall verdict.
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

1. `daemon status` prints engine-running, platform version, each protection's
   last result + recency, blocklist size, pf table size, skill-files-present,
   and an overall `HEALTHY` / `DEGRADED` / `DOWN` verdict.
2. The output (text and json) contains **none** of the disguised identifiers,
   even when a probe fails or the install is broken. This is enforced by a
   snapshot test fed deliberately poisonous values.
3. Under a user-only install the admin-level protections read **UNAVAILABLE
   (needs admin install)** — not down, not failed — and the overall verdict is
   `DEGRADED`.
4. Exit codes: `0` healthy · `1` degraded · `2` down · `3` internal error.
5. Querying a system install without sudo prints **"unknown (re-run with
   sudo)"** for the admin-only lines instead of hard-failing.
6. A fresh install (<10m, no runs recorded) reads `HEALTHY — warming up`.

## Honest limitations

- Status is a **read** of observed state; it is not itself a protection. A
  green status doesn't prove a determined bypass is impossible — only that the
  layers that should be up, are up. It is calibration, not enforcement.
- Without admin rights, the admin-level lines are genuinely unknown to the
  command; it says so rather than guessing. Run with sudo for the full read.
- Recency/age buckets are coarse (`<1m` / `<5m` / `<1h` / `>1h`) on purpose —
  precise timestamps add no operator value and risk fingerprinting.

## Non-goals

- No flag (`--show-paths`, `--debug`, `--verbose-internal`) that re-exposes
  the disguised identifiers. There is deliberately no escape hatch — that
  would reopen the exact leak this feature closes.
- Not a control surface. `status` never stops, starts, kills, or mutates
  anything; it only reads.
</content>
</invoke>
