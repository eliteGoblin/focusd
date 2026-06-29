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
> TC-08 (no false-degraded); the F15 config-integrity follow-up ↔ TC-10;
> **FEATURE 17 ↔ TC-14 (workdir delete), TC-18 (combinations), TC-19 (no
> generation pileup); FEATURE 18 ↔ TC-16 (Login-Item off), TC-17 (offline
> total-teardown, supersedes TC-05), TC-18; FEATURE 19 ↔ TC-20 (no tells).**
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
- **Deferred proper fix → NOW SPEC'D as [FEATURE 18](features/18-resilient-out-of-band-watchdog.md) (ADR-0020, approved-building 2026-06-29):**
  replace the cron rail with a **launchd-based out-of-band agent the daemon can
  manage without FDA**, plus a **signed offline engine backup** so the companion
  recovers offline. **TC-05 is superseded by TC-17 / TC-18** below (offline
  total-teardown recovery without FDA). Keep TC-05 here as the historical origin;
  verify the fix against TC-17/TC-18.

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

### TC-07 🟡 Status truthfulness — current-state only, no false-green  `[found 2026-06-22, refined 2026-06-22]`
- **Origin:** a no-op plugin that exits 0 makes `status` read `ok` over dead
  protection (the latent-failure class). The first fix (v0.16.1) made a
  since-repaired tamper read `tampered → repaired Nx` for 24h — but the owner
  found that misleading: a recovered, genuine, enforcing plugin still showed a
  persistent tamper verdict and dragged OVERALL to TAMPERED/DEGRADED for 24h.
  **Refined decision (KISS):** `status` reflects **current state only**; tamper
  **history** belongs in the log/events (the audit channel), not in status — don't
  mix the two.
- **Check (current-state truthfulness):**
  1. After a tamper, the plugin **auto-restores** and `status` returns to **ok** —
     because the restored plugin IS genuine and enforcing now (current state clean).
  2. The tamper appears in the **log + events** (the audit channel: F16 `WARN
     plugin tamper repaired …` line + platform event), **not** as a persistent
     status verdict.
  3. A **currently-unrestored** tamper (genuine binary not yet restored) **or** a
     real run-error reads **not-ok** — false-green is prevented by restore-before-run,
     not by a status flag.
  4. No persistent `tampered → repaired Nx` verdict remains in status; OVERALL is
     not dragged to TAMPERED/DEGRADED by a healthy, restored plugin.
- **Expect:** status truthful about the present; tamper history in log/events.
  **Status: ✅ PASS** (live v0.16.3, 2026-06-22): tampered kill-steam → restored →
  `status: kill-steam ok · OVERALL HEALTHY` (no `tampered` verdict); the tamper
  appears only in the log (`WARN plugin tamper repaired plugin=kill-steam …`) +
  event DB. OVERALL green for the recovered, healthy plugin.

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

### Live teardown matrix — *F17 / F18 / F19*  `[added 2026-06-29 — incident]`

> **NEVER claim "the daemon recovers the platform" from a single attack vector
> again.** The 2026-06-29 incident exposed a **latent failure**: the only
> recovery test ever run deleted the platform **binary** — and the desired-version
> state survived that delete, so recovery worked. The **workdir-delete** path
> (where the desired version is wiped too) was **never tested**, and on the real
> machine it caused a permanent `BLOCKED: no desired version` while games ran.
> Recovery must be proven against the **full matrix of teardown vectors AND their
> combinations** — each must auto-recover within a bounded time OR be a documented
> gap. A green from one vector is not recovery.

**Teardown vectors:** (a) delete platform **binary**, (b) delete the **workdir**
(wipes desired-version state), (c) **toggle off the Login Item** (unloads the
mesh; macOS gives no API to re-enable), (d) **kill all processes**, plus their
**combinations** (the worst is b+c+d = total atomic teardown).

### TC-14 🟠 Recover from workdir delete — *F17*  `[found 2026-06-29 — FAILING until F17]`
- **Origin:** the owner deleted the platform **workdir**; the desired version
  lived only in the workdir state, so the daemon logged `BLOCKED: no desired
  version` and **never re-fetched** — protection down, games ran. The latent gap
  the binary-only test missed.
- **Check:** delete the workdir → daemon falls back to the **baked platform
  version**, re-fetches, and brings protection back within a bounded time; **no
  permanent BLOCK**.
- **Expect:** auto-recovers, no permanent BLOCK. **Status: ⏳ pending (F17).**

### TC-15 🟠 Recover from binary delete (single vector — kept honest)  `[reframed 2026-06-29]`
- **Note:** this is the OLD recovery test. It passes because workdir state
  survives a binary delete. **It must never again be cited as proof that "the
  daemon recovers the platform"** — that claim requires the whole matrix (TC-14,
  TC-16, TC-17, TC-18).
- **Check:** delete only the platform binary → re-fetched + running within a
  bounded time.
- **Expect:** recovers. **Status: ✅ historically PASS (single-vector only).**

### TC-16 🔴 Recover from Login Item toggled off — *F17 / F18*  `[found 2026-06-29]`
- **Origin:** the owner toggled the Login Item **off**; this unloads the mesh and
  macOS provides **no API to re-enable it**, so protection stayed down.
- **Check:** with the Login Item toggled off, a surviving rail (the out-of-band
  companion, F18) re-establishes a working mesh within a bounded time.
- **Expect:** mesh back within bounded time. **Status: ⏳ pending (F18).**

### TC-17 🔴 Offline total-teardown recovery, no FDA — *F18 (supersedes TC-05)*  `[added 2026-06-29]`
- **Origin:** the deferred TC-05 — cron companion needed Full Disk Access, so it
  could neither self-heal nor be scripted-restored; it sat DOWN.
- **Check:** tear down the main mesh **+** the platform **binary** **+** the
  **workdir** all at once (total atomic teardown), with **no network**; the
  launchd-based companion (no FDA required) rebuilds them **offline** from its
  signed engine backup within a bounded time.
- **Expect:** rebuilt offline, no FDA, bounded time. **Status: ⏳ pending (F18).**

### TC-18 🟠 Teardown combinations matrix — *F17 / F18*  `[added 2026-06-29]`
- **Check:** exercise the combinations of {binary, workdir, Login Item, all
  processes} — not just one at a time — and confirm each combination either
  auto-recovers within a bounded time OR is recorded here as a documented gap.
- **Expect:** every combination recovers or is a named gap. **Status: ⏳ pending
  (F17/F18).**

### TC-19 🟠 No stale-generation pileup — single live generation — *F17*  `[found 2026-06-29]`
- **Origin:** repeated installs/recoveries stacked generations on disk (live
  count: 6 workdirs + 14 disguised supervisor entries), breaking the per-workdir
  singleton — **2 platform processes** ran at once — and piling up garbage.
- **Check:** after repeated installs/recoveries there is **exactly one** platform
  process and **one** live supervisor generation; superseded generations
  (workdirs / disguised entries) are **cleaned up**, count does not grow.
- **Expect:** one generation, one platform, no pileup. **Status: ⏳ pending (F17).**

### TC-20 🟡 Disguise — no obvious tells — *F19*  `[found 2026-06-29]`
- **Origin:** the owner spotted the install at a glance: `ps aux | grep mesh`
  found the role/marker; the Login-Items entries read as **three near-identical**
  `com.vendor.X.<hex>` siblings; and **many** stale records from old generations
  were visible.
- **Check (redaction-safe, counts only):** searching the live process list for the
  mesh marker **and** the role flags returns **nothing**; the login/background
  entries are **not** three near-identical hex-suffixed names (varied/plausible,
  don't cluster); stale records from prior generations are cleaned.
- **Expect:** no at-a-glance tells. **Status: ⏳ pending (F19).**

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
- **Expect:** quiet steady state. **Status: ✅ PASS** (live v0.16.2, 2026-06-22): 0 ERROR/WARN in the steady-state window.

### TC-13 🟡 Whitebox security-event log — *F16*  `[added 2026-06-22]`
- **Check:** after a tamper (TC-06), the app log contains the
  `plugin tamper repaired` WARN line naming the plugin — present **independently
  of `status`** (logged, not only DB-recorded). Redact: assert on event text +
  level + plugin id, never paths/labels.
- **Expect:** tamper event appears in the log. **Status: ✅ PASS** (live v0.16.2,
  2026-06-22): `level=WARN msg="plugin tamper repaired" plugin=kill-steam
  want_sha=… got_sha=…` — plugin id + sha prefixes only, no paths/labels.

---

## Run Log
| Date | Deploy | Pass | Fail | Notes |
|------|--------|------|------|-------|
| 2026-06-20 | daemon-v0.5.5 (F14) | TC-02, TC-03 | TC-05 | argv leak fixed live; watchdog recovery found broken |
| 2026-06-22 | (live restore) | TC-01 | TC-05, TC-06, TC-07, TC-08 | kill-steam tamper found + hand-restored; F15 fix pending |
| 2026-06-22 | platform v0.16.0→v0.16.1 (F15) | TC-01, TC-02, TC-03, TC-06, TC-07, TC-08, TC-11 | TC-05 | F15 plugin-integrity live-verified: tamper auto-restored (~6s) + surfaced in status (`tampered → repaired Nx`); deploy verified; watchdog recovery (TC-05) still open |
| 2026-06-22 | platform v0.16.2 (F16) | TC-11, TC-12, TC-13 | TC-05 (deferred) | F16 whitebox logging live-verified: steady-state log clean (TC-12) + tamper logged as `WARN plugin tamper repaired` independent of status (TC-13); watchdog (TC-05) deferred per owner |
| 2026-06-22 | platform v0.16.3 (status KISS) | TC-07, TC-08, TC-11 | — | status = current-state: recovered tamper → `ok`/OVERALL HEALTHY (no `tampered` verdict); tamper only in log/events; watchdog manually restored (`present`, never degrades). TC-05 still deferred (FDA) |
