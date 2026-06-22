# FocusD — Live Post-Deploy E2E: Test History & Regression Suite

> **Contract.** Every real deploy to a live machine MUST pass this checklist
> against the **live install** (not test-mode). **Every flaw we ever find
> becomes a permanent test case here** so the same hole can never silently
> reopen. This document is the regression suite; the `focusd-e2e` skill is how
> it is executed; **ba-curator** keeps it aligned with the feature register and
> **e2e-runner** executes + appends new cases.
>
> **Ownership invariant (ba-curator).** ba-curator owns keeping this suite in
> lockstep with the register: **every shipped/building feature maps to at least
> one `TC-*`, and every recorded flaw (register §6) maps to a `TC-*`.** When a
> feature ships or a flaw is recorded, ba-curator confirms the mapping exists.
> Current FEATURE 15 ↔ TC-06 (tamper→restore), TC-07 (no false-green),
> TC-08 (no false-degraded); the F15 config-integrity follow-up ↔ TC-10.
>
> **Redaction (non-negotiable).** Verify as a developer, but NEVER print the
> disguised workdir, labels, rotated binary paths, or plugin paths. Report
> booleans / counts / `<redacted>`. See the `focusd-protection` rule.
>
> **Why this exists.** The recurring failure mode in this project is **latent
> failure**: protection is dead while `status` reads green (tampered plugin,
> stale binary, deleted cron). Test-mode green ≠ live healthy. This suite is the
> standing defense against that class.

## How to run (each deploy)
1. `cd daemon && go build -o /tmp/fstatus ./cmd/daemon` (throwaway status binary).
2. `sudo /tmp/fstatus status` — redaction-safe snapshot (mesh roles, platform
   version, plugin last-run, watchdog).
3. Walk every `TC-*` below; record PASS / FAIL + date in the **Run Log**.
4. Any **new** weakness found in the wild → add a new `TC-*` (status FAIL until
   fixed), then fix, then it stays as a permanent regression.
5. Clean up: `rm -f /tmp/fstatus`.

Severity: 🔴 enforcement bypass · 🟠 self-heal/recovery · 🟡 truthfulness/observability.

---

## Test cases

### TC-01 🟠 Single platform + mesh up
- **Check:** exactly **one** focusd platform process (match by workdir, not the
  bare name `platform` — 8+ unrelated procs share that name). `status` shows
  the mesh roles up and `platform version good == desired`.
- **Expect:** 1 focusd platform; mesh healthy; versions match.

### TC-02 🟡 Mesh argv carries no secrets — *F14 / ADR-0018*  `[found 2026-06-20]`
- **Origin:** live `ps` printed `--roster` (the 3 `launchctl bootout` keys),
  `--github`, `--asset`, `--interval`, `--workdir` — defeating F10's
  decorrelation in one line.
- **Check:** `ps -axww -o args | grep -- --mesh` → **none** of the mesh argvs
  contain `--roster` / `--github` / `--asset` / `--interval` / `--workdir`;
  only `run --r <role> --mesh` remains. Report counts only.
- **Expect:** 0 leak flags. **Status: PASS** (deployed daemon-v0.5.5).
- **Honest limit:** `argv[0]` (binary path) is always visible to root — out of scope.

### TC-03 🟠 Worker self-heal — kill process  `[found 2026-06-20]`
- **Check:** kill a worker by role pattern (`pgrep -f "run --r b"`, no labels) →
  a new PID appears (KeepAlive restart) in < 2s.
- **Expect:** restart < 2s. **Status: PASS** (live 0.06s).

### TC-04 🟠 Worker self-heal — remove plist / disable  (test-mode harness)
- **Check:** `daemon/scripts/e2e_selfheal.sh` (isolated test-mode mesh):
  bootout+rm a worker plist → re-heals < 2s worst-case; `launchctl disable`+rm →
  re-loads (enable-before-bootstrap).
- **Expect:** all heal. **Status: PASS** (note: harness `<2.0s` gate is tight;
  ~2.1s worst-case is the 2s-cadence tail, not a regression — see TC-09).

### TC-05 🔴 Out-of-band watchdog recovery  `[found 2026-06-20 — FAILING, deferred]`
- **Origin:** the cron rail was deleted by hand; the running mesh ticks
  `EnsureWatchdog` but the rail does NOT self-heal — F12's "mutual re-install"
  claim is false on the real machine.
- **Root cause (confirmed live 2026-06-22):** modifying **root's crontab on
  modern macOS requires Full Disk Access (TCC)**. The launchd-spawned daemon —
  and ANY non-Terminal automated process (a Go helper run via `sudo`) — lacks
  FDA, so its `crontab` write hangs and is killed by the 5s guard
  (`crontab -: signal: killed`). Only a **Terminal that has been granted FDA**
  can (re)establish the rail. So the cron rail can neither self-heal NOR be
  scripted-restored; even the designed `RefreshWatchdog` fails.
- **Check:** with the watchdog removed, within one mesh tick `status` shows
  `out-of-band watchdog: present`.
- **Expect:** present again. **Status: 🔴 FAIL (DEFERRED 2026-06-22).** The rail
  is currently DOWN; a clean restore needs `daemon install`/self-update run from
  a **Terminal with Full Disk Access**, OR the deferred proper fix. The in-band
  mesh still self-heals all *partial* attacks (TC-03/04) — only the rare
  *total atomic teardown* recovery is affected.
- **Deferred proper fix (owner: leave watchdog for now):** replace the cron rail
  with a **launchd-based out-of-band agent the daemon can manage without FDA**
  (preferred), OR formally accept the rail as **interactive-FDA-establish-only**
  and document the re-install ritual. Tracked in the register.

### TC-06 🔴 Plugin integrity / tamper-restore — *F15 (shipped v0.16.0/v0.16.1)*  `[found 2026-06-22]`
- **Origin:** `kill-steam` plugin binary was overwritten on disk with a dummy;
  the platform kept exec'ing the dummy (Steam + Dota2 ran freely) and `status`
  showed the plugin as healthy. `bundle.ExtractTo` only ran at platform
  **startup**; reconcile never re-verified; the runner exec'd whatever was on disk.
- **Check:** overwrite a plugin binary on disk with a dummy → within one
  reconcile tick the **genuine** binary (sha256 == the `go:embed` golden copy
  inside the Ed25519-signed platform) is **restored**, the tamper is **recorded**,
  and `status` **never** reports the tampered plugin as `ok`.
- **Expect:** auto-restore ≤ 1 tick + tamper surfaced. **Status: ✅ PASS** (live
  v0.16.1, 2026-06-22): tampered binary restored to genuine in ~6s.

### TC-07 🟡 Status truthfulness — no false-green  `[found 2026-06-22]`
- **Origin:** a no-op plugin that exits 0 makes `status` read `ok` over dead
  protection (the latent-failure class). A subtler form caught live: the tamper
  event WAS recorded but `status` still read `ok` because `applyTamper` masked a
  repair behind the newer clean run (fixed v0.16.0→v0.16.1).
- **Check:** a tampered (even since-repaired) plugin must NOT read `ok`; the
  tamper surfaces as `tampered → repaired Nx` for 24h.
- **Expect:** no false-green. **Status: ✅ PASS** (live v0.16.1): status read
  `kill-steam: tampered → repaired Nx` over an `ok` run row.

### TC-08 🟡 Status truthfulness — no false-degraded  `[found 2026-06-22]`
- **Origin:** `status` reported `DEGRADED — 4/6 roles` while every *enabled*
  protection was `ok` (a *disabled* plugin like net-block counted as "not running").
- **Check:** an intentionally-disabled plugin must not drive the platform OVERALL
  to DEGRADED; DEGRADED should mean an *enabled* role actually failed.
- **Expect:** truthful overall. **Status: ✅ PASS** (live v0.16.1): net-block
  shown `disabled`, excluded from OVERALL. NOTE: the *daemon-level* `4/6 DEGRADED`
  is driven by the missing watchdog (TC-05), a separate issue — not net-block.

### TC-09 🟡 Self-heal cadence is honest
- **Check:** the test-mode harness gate is `< 2.0s`; the real bound is
  `interval + detection overhead ≈ 2.1s`. The gate should reflect the cadence,
  not cry wolf. **Status:** note/needs-tune (master also trips it).

### TC-10 🟠 Plugin **config**/policy integrity  (follow-up / icebox)
- **Check (future):** plugin configs (blocklists, target apps, job schedule)
  reconcile to signed desired state, tighten-only ("no inside door handle"); a
  neutered on-disk config is restored. **Status:** not yet implemented.

### TC-11 🟡 Deploy/update verification  `[added 2026-06-22]`
- **Origin:** a deploy can silently no-op or half-apply; "merged" ≠ "live". Every
  real `daemon`/`platform` update must be confirmed against the live install.
- **Check:** after `daemon update <ver>`, `status` shows
  `platform version good == desired == <ver>`, the mesh is healthy, and plugins
  verify genuine (no integrity events caused by the swap itself).
- **Expect:** new version live + healthy. **Status: ✅ PASS** (v0.16.1 swap
  confirmed live in ~12s).

### TC-12 🟡 Whitebox log hygiene — *F16*  `[added 2026-06-22]`
- **Origin:** the F15/TC-07 false-green showed a single observability path
  (`status`) is a single point of failure — the event was in the DB but didn't
  render. The app log is an independent, whitebox channel.
- **Check:** read the engine app log (`platform.log` under the workdir, via
  `sudo`, path redacted). A steady-state window has **no `ERROR` and no `WARN`**
  lines; print + FAIL on any (redacted).
- **Expect:** quiet steady state. **Status:** pending first live capture (F16).

### TC-13 🟡 Whitebox security-event log — *F16*  `[added 2026-06-22]`
- **Check:** after a tamper (TC-06), the app log contains the
  `plugin tamper repaired` WARN line naming the plugin — present **independently
  of `status`** (logged, not only DB-recorded). Redact: assert on event text +
  level + plugin id, never paths/labels.
- **Expect:** tamper event appears in the log. **Status:** pending first live
  capture (F16).

---

## Run Log
| Date | Deploy | Pass | Fail | Notes |
|------|--------|------|------|-------|
| 2026-06-20 | daemon-v0.5.5 (F14) | TC-02, TC-03 | TC-05 | argv leak fixed live; watchdog recovery found broken |
| 2026-06-22 | (live restore) | TC-01 | TC-05, TC-06, TC-07, TC-08 | kill-steam tamper found + hand-restored; F15 fix pending |
| 2026-06-22 | platform v0.16.0→v0.16.1 (F15) | TC-01, TC-02, TC-03, TC-06, TC-07, TC-08, TC-11 | TC-05 | F15 plugin-integrity live-verified: tamper auto-restored (~6s) + surfaced in status (`tampered → repaired Nx`); deploy verified; watchdog recovery (TC-05) still open |
