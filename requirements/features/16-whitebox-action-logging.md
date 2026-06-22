# FEATURE 16 — Whitebox action logging + log-based e2e verification

- **Status:** 🟡 building (2026-06-22)
- **Decision:** extends the register's "Observability is non-negotiable" principle; no new ADR (additive observability).
- **Maps to:** e2e-test-history TC-12 (log hygiene) + TC-13 (security events appear in logs)

## What

Every significant action the protection engine takes is written to the **app
log** as a structured, leveled line — and the live e2e suite verifies those
logs as a **whitebox** channel, independent of `status`.

- **Normal actions** (plugin run start/finish, reconcile tick, integrity check
  pass, version swap) log at **INFO**.
- **Problems** log at **WARN/ERROR**: a plugin **tamper → repaired** event, an
  integrity check/sweep failure, a job that couldn't run, a rollback.
- The e2e suite reads the app log and asserts: **a steady-state run has ZERO
  unexpected ERROR/WARN lines**; any that appear are **highlighted as a failure**;
  and **expected** security events (e.g. a tamper during the tamper test) **do
  appear** in the log.

## Why

The F15 live run exposed the risk: a plugin tamper was correctly recorded to the
state DB, but a gap in the **status rendering** meant it read `ok` anyway
(false-green) until we fixed it. Relying on a single observability path (status)
is a single point of failure. **The log is an independent, append-only channel.**
If the same event is also logged, e2e can verify it without trusting the status
code at all — and a silent ERROR/WARN that `status` doesn't surface gets caught.
This is the whitebox complement to the blackbox `status` checks.

It also lays the groundwork for the off-box heartbeat/accountability layer
(FEATURE 13) and the protection-coverage dashboard (icebox): both consume an
honest event stream, and the log is its first form.

## Acceptance criteria (observable)

1. Every significant engine action emits a structured, leveled log line
   (key=value fields), **redaction-safe** — event type, plugin id, sha
   *prefixes* only; **never** disguised workdir/labels/plugin paths.
2. A **clean steady-state run** (no tampering, all enabled plugins healthy)
   produces **no ERROR and no WARN** lines — only INFO.
3. A plugin **tamper** produces a **WARN/ERROR** line naming the event
   (`plugin tamper repaired`, plugin id, sha prefixes) — present in the log
   **independently of `status`**.
4. The `focusd-e2e` skill gains a **whitebox log step**: read the app log,
   FAIL on any unexpected ERROR/WARN (printing the offending lines, redacted),
   and confirm expected events are present.

## Honest limitations

- The app log lives under the disguised workdir; **redaction applies when e2e
  reads it** (assert on event text + levels, never print paths/labels).
- A root adversary can edit or delete the local log file — append-only is
  best-effort on-box; the log is **audit + friction + an e2e channel, not a
  tamper-proof record.** A tamper-proof event stream needs the off-box
  server/heartbeat layer (FEATURE 13 / icebox).
- "All actions" means **significant** actions (runs, reconciles, integrity,
  swaps, failures) — not a debug firehose; steady state stays quiet so WARN/ERROR
  is signal.
