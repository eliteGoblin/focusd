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
> generation pileup), TC-21 (post-recovery convergence — open follow-up);
> FEATURE 18 ↔ TC-16 (Login-Item off — PASS), TC-17 (offline
> total-teardown, supersedes TC-05 — PASS), TC-18; FEATURE 19 ↔ TC-20 (no tells),
> TC-23 (rebuild/watchdog path still leaks mesh role on argv — open follow-up);
> the F17/F19 convergence + status-flip follow-ups ↔ TC-21 (post-recovery
> convergence — PASS) + TC-22 (status never false-UNKNOWN — DB run-history
> concurrency — PASS) + TC-24 (churn-window status-vs-disk skew — WATCH).
> TC-23 confirmed open with decisive evidence (bug #83).
> **Hardening epic (2026-07-14, live-demonstrated bypasses — register §6
> limitation 13):** FEATURE 21 (HF1) ↔ TC-25 (platform-folder wipe can't disable
> the daemon; daemon re-establishes the platform — **PASS at the sandbox/test-mode
> tier on branch `hardening/hf1-storage-separation`, live-tier + deploy pending**);
> FEATURE 22 (HF2) ↔ TC-26
> (combined teardown recovers via out-of-band seed — issue #87); FEATURE 23 (HF3)
> ↔ TC-27 (dummy plugin binary cannot persist — **overlaps F15/TC-06, human to
> reconcile regression-vs-new-gap**); FEATURE 24 (HF4) ↔ TC-28 (no plugin/process
> identity tell maps to the working folder — **PASS at the sandbox/test-mode tier on
> branch `hardening/hf4-disguise`, live-tier + deploy pending**); **FEATURE 25
> (single-instance convergence + in-place upgrade) ↔ TC-29** (one daemon + one
> platform after install/upgrade; no CLI teardown door; steady-state reconcile reaps a
> duplicate — extends F17 + ADR-0013). **TC-26/27/29 TO VERIFY (not built);
> TC-25 + TC-28 PASS at the sandbox/test-mode tier (built, branches), live-tier +
> deploy pending — all sandbox/test-mode only, never the real install.**
> **VITAL bars (2026-07-14): FEATURE 24 (HF4) ↔ TC-30** — process-table & casual-probe
> invisibility of the workdir (nothing in argv **or** env locates it; no `--workdir`;
> no common word); **sharpens/supersedes TC-28's narrower sweep** (TC-28 didn't
> exercise the `ps -E` env-dump vector, so the env-var `--workdir` leak survives —
> current build **PRE-FIX**, TC-30 **OPEN**). **FEATURE 21 (HF1) / FEATURE 17 ↔ TC-31**
> — deleting the working folder (`rm -rf`) self-recovers a **FRESH** artifact (new
> mtime/pid), no manual help, bounded window; consolidates the VITAL recovery bar
> already part-covered by TC-14 (live-PASS) + TC-25 (test-mode PASS); **OPEN**,
> live-tier. **FEATURE 25 × FEATURE 24 ↔ TC-32** — upgrade end-state: exactly one
> fresh disguised process, zero old/duplicate/orphan, no leftover pre-disguise
> `--workdir` line, and the survivor passes the TC-30 bar (no-duplicate-on-upgrade is
> **both** a resilience property F25 **and** a disguise property F24); **OPEN**. All
> three are standing red-team + e2e gates, each verified TEST-MODE first THEN LIVE.
> **Anti-tamper VITAL (2026-07-14, layered supervision — register §5):** **TC-33** — a
> tampered platform binary (empty+chmod+x / fake / delete) **should** auto-revert to the
> genuine **signed platform** before it runs (daemon→platform) — **test-mode FAIL
> (empirical):** a **fake** platform binary was **exec'd UNVERIFIED** and an **empty** swap
> took the platform **DOWN** (verify gated on binary-presence, no sig-check on the exec
> path); **verify-before-exec fix IN PROGRESS — stays OPEN until it re-tests PASS**.
> **TC-34** — a tampered **bundled** plugin binary auto-reverts to the genuine **embedded**
> binary before it runs (platform→plugins; HF3 / FEATURE 23, relates TC-27 + TC-06/F15 —
> human to reconcile the F15-vs-F23 framing) — **test-mode PASS** (all 3 variants restore
> genuine before run, fake never runs, tamper event recorded; **live e2e still owed** for
> final sign-off). Scope: signed platform + **bundled** plugins only — **unbundled**
> browser-monitor (anti-tamper-GAP in **user** mode; **closed in system mode** by the
> bundled-only allowlist, verify live) / freedom-protector are NOT covered by HF3.
> Evidence: `scripts/testmode/tc-layered-antitamper.sh`.
> **FEATURE 20 (mac-browser-guard) ↔ TC-U1 — a UTILITY-tier case (ADR-0021),
> kept OUTSIDE the platform mesh suite: it exercises a user-mode script on a
> different execution path, not the signed daemon/platform mesh, so its run model
> is not the mesh "walk every TC each deploy" model — see the Utility-tier
> section below.**
>
> **v0.18.0 consolidated-hardening LIVE record (2026-07-15).** First live verification
> of the consolidated build: **TC-30 PASS live (FEATURE 24 VITAL invisibility bar MET —
> workdir in neither argv nor env)**, **TC-32 PASS live** (one fresh disguised instance
> after upgrade, respawns, no old `--workdir` line), **TC-33 PASS live via the restart
> path** (verify-before-exec — tampered platform never runs unverified), plus signed
> release + one-generation confirmed. **Still open:** **TC-31 FAIL** (wiped folder
> self-heals only on platform restart, not proactively → FEATURE 21 stays BUILDING),
> and **NEW TC-35 FAIL** (`watchdog_copy_ok=false` — the FEATURE 18 companion offline
> backup rail is broken; needs a bug ticket). New DEFINING specs mapped: **FEATURE 26
> (disguise blend-in) ↔ TC-36** (TO VERIFY) and **FEATURE 27 (browser-monitor
> standalone) ↔ TC-U2** (utility tier, TO VERIFY). Observation: net-block disabled by
> baked default + config/policy integrity ABSENT (TC-10 iceboxed; config→server
> direction, register §9).
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
- **Expect:** auto-recovers, no permanent BLOCK. **Status: ✅ PASS** (live +
  peer-reviewed, daemon-v0.5.6 / platform v0.16.4, 2026-06-29): deleting the
  live workdir took the platform down with `desired=none` (~28s), then the baked
  fallback version was adopted (`desired=v0.16.3`), re-fetched + verified, and the
  platform was back up (~56s) — no permanent BLOCK. go-reviewer confirmed via code
  that the fallback-adopt + workdir-recreate path is genuine (not a surface green).
- **Methodology lesson — gate destructive e2e steps on a positive existence check
  of the EXACT target.** The FIRST attempt was a **false-pass**: a split-on-space
  bug truncated the target path so the delete missed entirely and "recovery"
  proved nothing. It was caught by a `test -d` existence gate on the exact target
  before the real run. Any destructive teardown step must first assert the precise
  thing it intends to destroy actually exists, or a no-op masquerades as a pass.

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
- **Expect:** mesh back within bounded time. **Status: ✅ PASS** (live,
  daemon-v0.5.9, 2026-06-29): the separate launchd companion (own folder, no FDA)
  detected the stale daemon heartbeat and rebuilt the daemon → mesh + platform back
  from its signed offline backup, no manual action.

### TC-17 🔴 Offline total-teardown recovery, no FDA — *F18 (supersedes TC-05)*  `[added 2026-06-29]`
- **Origin:** the deferred TC-05 — cron companion needed Full Disk Access, so it
  could neither self-heal nor be scripted-restored; it sat DOWN.
- **Check:** tear down the main mesh **+** the platform **binary** **+** the
  **workdir** all at once (total atomic teardown), with **no network**; the
  launchd-based companion (no FDA required) rebuilds them **offline** from its
  signed engine backup within a bounded time.
- **Expect:** rebuilt offline, no FDA, bounded time. **Status: ✅ PASS (Phase 1)**
  (live, daemon-v0.5.9, 2026-06-29): the entire in-band rail was removed (daemon
  down, 0 platforms, 0 mesh plists); the separate launchd companion detected the
  stale heartbeat and **rebuilt the daemon from its signed offline backup (7.9MB,
  signature-checked) → mesh + platform back**, no FDA. The companion is excluded
  from mesh discovery/cleanup by construction (verified). **Phase 2 (offline
  *platform* restore) deferred:** the companion carries the daemon backup only; the
  daemon re-fetches the platform over the network — a fully offline platform restore
  is a later phase.

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
- **Expect:** one generation, one platform, no pileup. **Status: ✅ PASS** (live,
  daemon-v0.5.6 / platform v0.16.4, 2026-06-29): a fresh F17 install logged
  "retired 4 prior generation(s)" and converged to exactly **1** platform process.
  Install-time generation cleanup (signature-gated bootout + kill + remove) works.
- **Scope limit (see TC-21):** install-time cleanup only retires generations whose
  binary still verifies. A generation whose **workdir was wiped** (binary deleted)
  is NOT retired by install cleanup — that residual gap is tracked as **TC-21**.

### TC-20 🟡 Disguise — no obvious tells — *F19*  `[found 2026-06-29]`
- **Origin:** the owner spotted the install at a glance: `ps aux | grep mesh`
  found the role/marker; the Login-Items entries read as **three near-identical**
  `com.vendor.X.<hex>` siblings; and **many** stale records from old generations
  were visible.
- **Check (redaction-safe, counts only):** searching the live process list for the
  mesh marker **and** the role flags returns **nothing**; the login/background
  entries are **not** three near-identical hex-suffixed names (varied/plausible,
  don't cluster); stale records from prior generations are cleaned.
- **Expect:** no at-a-glance tells. **Status: ✅ PASS** (live, daemon-v0.5.7,
  2026-06-29): e2e-verifier confirmed searching the live process list for the mesh
  marker **and** the role flags returns **0** hits (role/marker moved off argv into
  the plist environment); the supervisor labels are varied/plausible and **do not
  cluster** as a near-identical hex triplet. `argv[0]` (binary path) stays visible
  to root by design (honest limit).

### TC-21 🟡 Post-recovery generation convergence — no invisible zombies / orphan platforms — *F17 follow-up*  `[found 2026-06-29 — FAILING]`
- **Origin (peer-reviewed, confirmed real):** after **workdir-delete/recovery
  cycles**, orphan generations accumulate that F17's install-time cleanup **cannot
  retire**. Two reinforcing mechanisms (go-reviewer, with code evidence): (1) the
  generation discovery step requires each supervisor entry's binary to pass
  signature verification, but a generation whose workdir was wiped has a **deleted
  binary** → the read fails → that generation is **silently skipped** and its
  supervisor entry + supervised platform persist **invisibly**; (2) deleting a
  workdir does **not** kill the already-running platform process (binary unlinked
  but process alive), and the daemon intentionally leaves the platform running on
  shutdown (protection continuity) — so each singleton-lock handover starts a
  **new** platform and leaves the old one → unbounded accumulation. **Observed
  live:** cleanup logged "retired 1" while **2 live generations / 3 supervised
  platforms** remained; only a manual clean-slate reached 1.
- **Honest impact:** protection stays **HEALTHY** (more enforcers, not fewer) — this
  is a **hygiene/observability** gap (multi-platform, multi-generation clutter and a
  visible tell), **not** a protection bypass. Manifests **only** after
  teardown/recovery cycles, not in steady state.
- **Check:** after workdir-delete/recovery cycles, there is **exactly one** platform
  process and **one** live generation; generations whose binary was deleted are
  retired (entry removed + their platform process ended), not left as invisible
  zombies.
- **Fix direction (actionable):** treat supervisor entries pointing at **deleted**
  binaries as **dead generations** (don't require signature verification when the
  binary is absent) and have the retire step boot them out + remove their entry +
  end their orphaned platform process, gated by the existing safe-to-remove check.
- **Expect:** converges to one generation / one platform after recovery cycles.
  **Status: ✅ PASS** (live, daemon-v0.5.8 orphan-sweep #76, 2026-06-29): after
  recovery/install churn there is **exactly 1 state.db + 1 platform** — the
  orphan-sweep retires dead-binary/zombie generations and sweeps their superseded
  on-disk workdir/state files (closing prior sub-gap (a)). The status-flip sub-gap
  (b) is closed separately under **TC-22** (status snapshot, platform v0.16.7). The
  manual cleanup helper (c) is now F19-env-aware. Convergence is clean live.

### TC-22 🟡 Status never falsely reads OVERALL UNKNOWN — run-history DB concurrency  *F17/F19 follow-up*  `[found 2026-06-29 — FAILING]`
- **Origin:** during install/convergence churn `status` intermittently flipped to a
  display showing **OVERALL UNKNOWN** even though protection was genuinely healthy
  and enforcing. Root-caused (NOT a multi-generation artifact) to a **concurrent
  read/write of the plugin run-history in the state DB**: a `status` read racing a
  reconcile write returned a partial/empty run-history, which rendered as UNKNOWN —
  a **truthfulness/observability** false-signal of the latent-failure class (status
  lying about a healthy system, this time pessimistically).
- **Check:** under install/convergence churn (and steady state), `status` reflects
  the **true current health** — a healthy, enforcing system never renders OVERALL
  **UNKNOWN**; concurrent `status` reads during a reconcile write return a complete,
  consistent run-history.
- **Expect:** no false UNKNOWN; status read is consistent under concurrency.
  **Status: ✅ PASS** (live, platform v0.16.7 #79, 2026-06-29): 8 rapid `status`
  reads were stable (kill-steam ok, OVERALL HEALTHY, 0 changes) with no false
  UNKNOWN. **Root cause:** the `status` command's cross-process read contended with
  the reconcile writer; **fix:** `status` now reads an **atomic snapshot**, not the
  live DB. (Three earlier attempts — orphan-sweep, WAL, rollback-journal — are
  documented dead-ends; the snapshot is what closed it.)

### TC-23 🟡 Rebuild/watchdog path leaks the mesh role on argv — *F19 follow-up*  `[found 2026-06-29 — FAILING]`
- **Origin:** FEATURE 19 moved the mesh role/marker off the command line for the
  `daemon install` path, but the **watchdog/rebuild path** (used by the FEATURE 18
  companion + legacy recovery) does **not** route through the same env-based setup.
  After the companion rebuilt the daemon, `ps` showed `run --r a --mesh` /
  `run --r b --mesh` again — so the at-a-glance `grep`-for-mesh tell returns. A clean
  `daemon install` clears it (leak = 0).
- **Honest impact:** the tell returns **only** after an out-of-band recovery, and
  only until the next clean install. Hygiene/friction grade, **not** a bypass —
  protection stays healthy.
- **Check (redaction-safe, counts only):** after a companion/watchdog rebuild,
  `ps -axww -o args | grep -- --mesh` (or a grep for the role flags) returns **0**.
- **Fix direction:** route the watchdog/rebuild mesh-install through the same F19
  env-carried plist path that `daemon install` uses, so every rebuild path is
  leak-free by construction.
- **Expect:** 0 mesh hits after rebuild. **Status: 🟡 FAIL (confirmed — decisive
  evidence)** (live, daemon-v0.5.10, 2026-06-29) — bug **#83**. Direct plist read
  after a companion rebuild: rebuilt mesh plists with `--mesh` in ProgramArguments
  = **2**, plists carrying the F19 env marker = **0** → the rebuild writes
  **pre-F19-format** plists. Two prior theories **refuted**: NOT test-mode (the prod
  daemon rejects `--test-mode`; the release was built without the e2e tag; mode
  resolves to System, never Test) and NOT a stale backup (the backup is
  daemon-v0.5.9, which already carries F19). **Root cause not yet pinned.**
  Hygiene/friction (protection stayed HEALTHY; a clean install clears the leak → 0).
  Fix direction unchanged: route the rebuild mesh-install through the F19 env path
  — see #83.

### TC-24 🟡 Status never reads green while the disk is empty — churn-window status-vs-disk skew — *F17/F18 observability*  `[found 2026-06-29 — WATCH]`
- **Origin:** during teardown/install churn windows, `status` printed
  **"3/3 roles running"** while the on-disk live-generation count = **0** for one
  sample; it **self-corrected on the next sample**. This is the **latent-failure
  class** (status green while the disk is empty) — the same shape as TC-22, but in
  the generation/disk dimension rather than the run-history DB. Observed bounded to
  the churn window, not steady state.
- **Check:** during teardown/install churn (and in steady state), `status` never
  reports roles running while the on-disk live-generation count is 0 — a green
  status always corresponds to a real on-disk generation.
- **Expect:** no green-over-empty-disk skew. **Status: 🟡 WATCH** (noted live,
  daemon-v0.5.10, 2026-06-29): observed once during a churn window, self-corrected
  the next sample → recorded so the observation isn't lost; **FAIL if reproduced or
  sustained**.

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

## Hardening-epic test cases (HF1–HF4) — *FEATURE 21–24*  `[added 2026-07-14 — live-demonstrated bypasses]`

> These map the four bypasses the owner demonstrated **live** (register §6
> limitation 13). TC-26/27/28 are **TO VERIFY (not built)**; **TC-25 is PASS at the
> sandbox/test-mode tier** (built on branch `hardening/hf1-storage-separation`),
> with live-tier follow-ups + an actual deploy still pending. **TC-30 (VITAL
> process-table invisibility) and TC-31 (VITAL workdir-delete fresh recovery) are
> OPEN standing gates** — TC-30 sharpens TC-28 (current build PRE-FIX: the
> `--workdir` env-var leak survives the `ps -E` env dump). **Verify each on a
> sandbox/test-mode instance ONLY — never by tearing down the owner's real
> install.** A sandbox/test-mode PASS is a real failure-and-recover run on a
> disposable instance (baseline-OPEN-on-master → CLOSED-on-fix), **not** test-mode
> green alone; it is an intermediate tier — a feature ships only after its
> **live-tier** follow-ups pass on the real install + an actual deploy (§5).

### TC-25 🟢 Platform-folder wipe can't disable the daemon; daemon re-establishes the platform — *FEATURE 21 (HF1)*  `[found 2026-07-14 — TEST-MODE PASS, live-tier pending]`
- **Origin:** deleting the platform working folder took the **whole system** down,
  including the daemon meant to rebuild the platform (recoverer shares fate).
- **Check (sandbox/test-mode only):** remove the platform's working storage →
  the daemon's own identity + storage stays present, the daemon stays running, and
  it re-establishes the platform **fresh** within a bounded, stated window with no
  manual action.
- **Expect:** daemon survives + platform back, fresh, bounded time.
- **Status: 🟢 PASS at the SANDBOX / test-mode tier — NOT yet live-verified**
  (branch `hardening/hf1-storage-separation`, fix commit `f0593fb`, 2026-07-14).
  Baseline-then-fix, both weaknesses reproduced **OPEN on master** and **CLOSED on
  the fix**, twice each, clean isolation:
  - **25a** (shared-fate): master baseline — deleting the platform folder **KILLS
    the daemon** (binary co-located) [OPEN]; fix — daemon **SURVIVES** and
    **re-establishes the platform at a FRESH path** (new inode/mtime/sentinel)
    [CLOSED]. Evidence: pids `67149→67339`; new platform-workdir path P1≠P0; the
    daemon binary in daemon-home survived the wipe. (Acceptance 1, 2, 3.)
  - **25b** (independent storage): daemon-home ≠ platform-workdir — **sibling
    roots, neither nested** in the other [PASS].
  - **25c** (blast-radius, CRITICAL): a decoy real-install dir (hidden-dot +
    state.db) under `~/Library` was **DELETED on master** by `install`, but
    **SURVIVES on the fix** (sweep scoped to the explicit support root) [PASS —
    the delete-danger is fixed].
  - **25d** (no regression): clean instance — single supervisor, mesh mutual-respawn
    intact [PASS]. (Acceptance 4.)
  - **25e** (truthfulness): status reports **down honestly**, no green-over-dead [PASS].
- **Reusable script:** `scripts/testmode/TC-25-storage-separation.sh` (safe_rm-gated —
  every destructive step asserts the exact target exists first).
- **Honesty caveat (why this is a tier, not done):** test mode does **not**
  auto-relocate the binary, so the baseline **emulated** the production single-root
  shared-fate rather than exercising the real relocate. The sandbox proves the
  separation logic; **live must confirm the real relocate.**
- **Live-tier follow-ups (for the e2e-verifier, before FEATURE 21 can ship):**
  1. a real disguised generation-workdir under the **actual** `~/Library` survives
     a **real** install (real relocate, not emulated);
  2. status stays truthful when a **genuinely-running** platform's workdir is
     deleted (no green-over-dead with a live platform).

### TC-26 ⬜ Recovery survives combined teardown (auto-start off + folder wiped) via an out-of-band seed — *FEATURE 22 (HF2)*  `[found 2026-07-14 — TO VERIFY]` (issue **#87**)
- **Origin:** disabling auto-start (login item) **AND** deleting the platform folder
  stopped everything and did not reliably recover.
- **Check (sandbox/test-mode only):** with auto-start disabled **and** the platform
  folder removed, protection re-establishes within a bounded window; the recovery
  seed is **not discoverable by enumerating running processes**; the recovered
  install is a **fresh rebuild**, not a survivor; disabling any single start
  mechanism does not prevent recovery.
- **Expect:** combined teardown recovers, seed out-of-band, fresh, redundant starts.
  **Status: ⬜ TO VERIFY (not built).** Relates to issue **#87**.

### TC-27 ⬜ Dummy plugin binary cannot persist — re-verify every cycle, refuse + re-fetch — *FEATURE 23 (HF3)*  `[found 2026-07-14 — TO VERIFY]`
- **Origin:** a plugin binary swapped with a dummy **persisted and ran** (false-green
  enforcement bypass). **⚠️ Overlaps FEATURE 15 / TC-06, recorded PASS** — reconcile
  with the human whether this is a regression of F15 or a new gap.
- **Check (sandbox/test-mode only):** replace a plugin binary with a different
  (dummy) one → detected on the **next reconcile cycle** (not install-only), refused
  (never run), replaced from the signed source; the dummy does **not** persist across
  cycles; the tamper is recorded (audit); health does not read ok while unrestored.
- **Expect:** dummy detected + refused + re-fetched within a bounded number of
  cycles; recorded; no false green. **Status: ⬜ TO VERIFY (not built).**

### TC-28 ⬜ No plugin/process identity tell maps to the working folder — *FEATURE 24 (HF4)*  `[found 2026-07-14 — TO VERIFY]`
- **Origin:** `grep` of the process list for the target app revealed the
  process-killer; a process name/version (e.g. `v0.16`) led the owner to the working
  folder.
- **Check (sandbox/test-mode only, redaction-safe counts):** a search of the live
  process list for the enforcement keywords (target-app name, "kill", "platform")
  **and** for a version string returns **0** hits that identify the install; plugin
  argv carries zero identifying info; no running-process string maps to the working
  folder.
- **Expect:** 0 identifying hits; argv clean; no path to the folder.
  **Status: 🟢 PASS at the sandbox/test-mode tier** (branch `hardening/hf4-disguise`,
  commit `f5a157c` on the HF1 base). Zero-leak greppability checks (criteria 1–3)
  come up **empty** — a sweep of the running system for the enforcement keywords, a
  version string, "focusd"/"platform", plugin names, and the working-folder location
  finds nothing that maps to the install; the platform process shows a generic name,
  plugin config is carried off the command line, on-disk plugin folders + logs use
  neutral names, and mesh labels no longer cluster. **NOT live-verified, NOT
  deployed** — a live e2e pass on the real install + a signed deploy remain (done in
  the hardening-epic consolidation). **Honest limit:** casual-grade only — `argv[0]`
  (the binary path) stays root-visible via low-level APIs; the goal is only that
  nothing *greppable* maps to the install in a `ps`/`grep` sweep.

### TC-29 ⬜ One daemon + one platform after install/upgrade; no CLI teardown door — *FEATURE 25*  `[found 2026-07-14 — TO VERIFY]`
- **Origin:** duplicate accumulation observed live (~16 "platform" processes at one
  point) + stale old-version daemons left after upgrades — upgrades stacked instead
  of replacing. Extends FEATURE 17 (generation retirement) + ADR-0013 (single-platform
  lock).
- **Check (sandbox/test-mode only, redaction-safe counts):**
  (1) after an install/upgrade → exactly **one** daemon generation + **one** platform
  process, no orphans / no old-version daemon;
  (2) install over an existing install → prior generation(s) retired + the daemon
  binary **replaced in place** (not a second live stack);
  (3) introduce a **second/duplicate** platform at steady state → the reconcile loop
  **retires it** (returns to one) unprompted, within a bounded number of cycles;
  (4) **no CLI verb** exists that a user can invoke to stop / clean up / kill
  protection — the convergence is internal to install/reconcile; the uninstall ritual
  is the sole sanctioned removal;
  (5) no regression to recovery/self-heal (TC-14/16/17) or enforcement.
- **Expect:** exactly one daemon + one platform at steady state; in-place upgrade;
  planted duplicate reaped; no stop/cleanup command; no regression. A transient
  handover-overlap window during upgrade is acceptable (steady-state guarantee).
  **Status: ⬜ TO VERIFY (not built).** See **TC-32** for the upgrade end-state
  coupled with the disguise/invisibility bar (single-instance is also a disguise
  property, not only resilience).

### TC-30 🟠 Process-table & casual-probe invisibility — nothing locates the workdir; no `--workdir` in argv/env — *FEATURE 24 (HF4), **VITAL***  `[found 2026-07-14 — ✅ PASS live 2026-07-15, v0.18.0]`
- **Origin (the owner's single most-used exploit):** `grep` the process list for a
  meaningful word → **read the workdir path off the process** → `rm -rf` it →
  protection down. The `--workdir` **argument** was the tell; the half-fix only moved
  it into an **environment variable**, which the environment-dump form of `ps` still
  prints. Closing this **grep → workdir → `rm`** chain is a core friction
  requirement, not cosmetic.
- **Check (redaction-safe counts) — the literal contract:** the
  exploit `sudo ps aux | grep -i support | grep -i application | grep '<release-tag>'`
  **and** its `ps -E` / `ps eww` (environment dump), `ps -o comm`, `find`, and `lsof`
  variants each return **0** hits that locate the working directory or identify the
  binary. `--workdir` appears as **neither** a command-line argument **nor** an
  environment variable in a production build. No common word — the target-app name,
  `platform`, `daemon`, `block`, `dns`, `kill`, `focusd`, or the source-repo path /
  release tag — appears in the process list or in on-disk names.
- **Human-effort bar (the realistic threat).** A technically-competent adversary (the
  owner) spending **10–20 minutes MANUALLY typing commands** — `ps aux`, `sudo ps -E`
  / `ps eww`, `ps -o comm`, `grep` by any common word, `find`, `lsof`, a `launchctl`
  listing — i.e. a realistic human probing session, **NOT** an exhaustive automated
  sweep — must find **NO easy path** to the working directory or the process identity.
  **If the human locates the working folder within that window, it is a FAIL** (they
  will then delete it). **PASS = no easy leak in a 10–20 min manual session.**
- **Expect:** 0 hits from every probe; no `--workdir` in argv or env; no common-word
  tell; no easy path found by hand in 10–20 min. **Status: ✅ PASS — live-verified
  (v0.18.0 consolidated hardening build, 2026-07-15).** The workdir appears in
  **neither argv nor env** on the live build — the env-dump (`ps -E`) form no longer
  prints a `--workdir` env var, closing the leak TC-28 never exercised. VITAL
  invisibility bar **MET live** (FEATURE 24). Sharpens/supersedes **TC-28**, whose
  earlier test-mode PASS did **not** exercise the `ps -E` environment-dump vector.
  **Blend-in naming quality (FEATURE 26 / TC-36) is the next disguise bar** — this TC
  closes the process-table leak, not the on-disk matching-set/hex-suffix signature.
- **Verification order (BOTH stages required to close):** verified in **TEST-MODE /
  sandbox FIRST** (testmode-verifier + the red-team manual sandbox session), **THEN
  confirmed on the LIVE deploy** (e2e-verifier re-runs the same probes + the manual
  session against the real running install). **A test-mode pass alone does NOT close
  this TC** — the live confirmation is required (test-mode green ≠ live healthy). The
  live probe here is read-only (`ps`/`find`/`lsof`/`launchctl` listing), so it needs
  no teardown of the owner's install. Flip to PASS **only** on a real build where
  every probe returns nothing at **both** stages.

### TC-31 🟠 Deleting the working folder self-recovers a FRESH artifact — *FEATURE 21 (HF1) / FEATURE 17, **VITAL recovery***  `[found 2026-07-14 — 🔴 FAIL live 2026-07-15, v0.18.0]`
- **Origin:** the working folder is the obvious `rm -rf` target once found (TC-30);
  deleting it must not permanently break protection — the supervisor must
  re-establish it **fresh** from the baked fallback + daemon/platform storage
  separation (FEATURE 17 + 21).
- **Check:** `rm -rf` the working folder → protection self-recovers with **no manual
  help** within a **bounded, stated window**, and the recovered artifact is **FRESH**
  (new mtime / new pid), not a survivor. Must be **exercised, not assumed**
  (existence-gate every destructive step on the exact target first).
- **Human-effort bar (the realistic threat).** A technically-competent adversary (the
  owner) spending **10–20 minutes MANUALLY typing commands** — `ps aux`, `sudo ps -E`
  / `ps eww`, `ps -o comm`, `grep` by any common word, `find`, `lsof`, a `launchctl`
  listing — i.e. a realistic human probing session, **NOT** an exhaustive automated
  sweep — must find **NO easy path** to the working directory or the process identity.
  **If the human locates the working folder within that window, it is a FAIL** (they
  will then delete it). **PASS = no easy leak in a 10–20 min manual session** — and if
  they do delete it, recovery restores a FRESH artifact within the window (unaided).
- **Expect:** fresh recovery (new mtime/pid), no manual action, bounded window.
  **Status: 🔴 FAIL — live (v0.18.0, 2026-07-15).** A wiped working folder is **not
  proactively self-healed**: recovery happens **only on the next platform restart**,
  not on its own while the platform is running — so a deleted folder can stay down
  until something else restarts the platform. The VITAL recovery bar is **NOT met**;
  **FEATURE 21 stays BUILDING, not shipped.** Consolidates the VITAL recovery bar
  part-covered by **TC-14** (platform-workdir delete → baked-fallback recovery on
  restart, live-PASS) and **TC-25** (daemon survives the platform-folder wipe +
  re-establishes fresh — test-mode PASS). Fix direction: proactive self-heal of the
  wiped folder within a bounded window without waiting for a restart.
- **Verification order (BOTH stages required to close):** verified in **TEST-MODE /
  sandbox FIRST** — the destructive `rm -rf` runs on a **disposable** instance
  (testmode-verifier + red-team sandbox run), existence-gated — **THEN confirmed on
  the LIVE deploy** (e2e-verifier exercises the real-deploy recovery, as TC-14 did:
  the wiped folder self-recovers fresh, so it does not permanently harm the
  commitment device). **A test-mode pass alone does NOT close this TC** — the live
  confirmation is required (test-mode green ≠ live healthy). Flip to PASS only when a
  FRESH artifact returns unaided within the window at **both** stages.

### TC-32 🟠 Upgrade end-state — clean single-instance AND unidentifiable — *FEATURE 25 (single-instance) × FEATURE 24 (disguise), **VITAL***  `[found 2026-07-14 — ✅ PASS live 2026-07-15, v0.18.0]`
- **Why coupled (the rationale):** a leftover **old, un-disguised** process after an
  upgrade is **doubly harmful** — it leaks the old identity/workdir (the pre-disguise
  `platform --workdir` line) **and** hands the owner a folder to delete, defeating the
  disguise on the new process; and two near-identical processes is itself a disguise
  **tell**. So "no duplicate/old process on upgrade" is **both** a resilience property
  (F25) **and** a disguise property (F24) — they must pass **together**.
- **Check (redaction-safe counts):** after upgrading from the prior version, `ps` shows
  **EXACTLY ONE** current, disguised process — **ZERO** old-version / duplicate
  processes, no orphans, and specifically **NO** leftover pre-disguise
  `platform --workdir` line from the old generation. The surviving process **satisfies
  the TC-30 invisibility bar + the 10–20-min human-effort bar**.
- **Expect:** exactly one fresh disguised instance; zero old/duplicate/orphan; no old
  `--workdir` tell; the survivor passes TC-30.
- **Verification order (BOTH stages required to close):** **TEST-MODE / sandbox
  FIRST** — stand up a prior-style generation, install/upgrade to the new build,
  confirm the old generation is retired and exactly one fresh disguised instance
  remains; **THEN LIVE** — deploy the consolidated build over the current pre-disguise
  install (platform v0.16.7): baseline-then-verify that the old process is gone and
  exactly one new disguised process is present. **A test-mode pass alone does NOT
  close this TC.**
- **Status: ✅ PASS — live-verified (v0.18.0 consolidated hardening build,
  2026-07-15).** After the upgrade the process table shows **exactly one** fresh
  disguised instance — **zero** old/duplicate/orphan processes, and it **respawns**
  when killed (single-instance held live); no leftover pre-disguise `--workdir` line,
  and the survivor satisfies the TC-30 invisibility bar. The F25 reaper recognised the
  **disguised** new identity (register §9 F25↔HF4 coupling) and retired the old
  generation instead of leaving two live stacks. Couples **TC-29** (F25 single-instance
  / in-place upgrade / reaper) with **TC-30** (F24 invisibility bar). *Note: the e2e
  handoff labelled this "respawn PASS"; recorded here as the upgrade-end-state
  single-instance-×-disguise coupling — confirm if a narrower reading was intended.*

### TC-33 🔴 Tampered platform binary should auto-revert to the genuine signed platform before it runs — *Layered supervision (daemon→platform), **VITAL anti-tamper***  `[found 2026-07-14 — ✅ PASS live 2026-07-15 (restart path), v0.18.0]`
- **Origin (anti-tamper invariant, register §5):** the daemon owns the platform's
  **authenticity**, not just its liveness — a running-but-tampered platform is a
  false-green. This case pins that the daemon **verify-or-restores** the platform before
  it runs.
- **Check (sandbox/test-mode first, THEN LIVE, redaction-safe):** tamper the on-disk
  platform binary three ways — **(a) emptied + `chmod +x`** (zero-byte executable),
  **(b) swapped** for a different/dummy binary, **(c) deleted** — then let the daemon run
  its next cycle. In each case the tampered/absent binary is **refused (never run)** and
  the daemon **restores / re-fetches the genuine signed platform** (signature verified)
  before the platform executes.
- **Expect:** none of the three tamper variants runs; the genuine signed platform is what
  actually runs, restored within a bounded window; no false green while unrestored.
- **Status: ✅ PASS — live-verified (v0.18.0 consolidated hardening build,
  2026-07-15), via the restart path.** The verify-before-exec fix landed: a tampered
  platform binary is **no longer exec'd unverified** — the daemon restores / re-fetches
  the genuine **signed** platform and it is that genuine binary which runs. Recovery is
  via the **restart path** (tamper detected → restart into the verified genuine
  platform) rather than fully inline. Reverses the 2026-07-14 empirical test-mode FAIL
  (a **fake** binary had run unverified; an **empty** swap took the platform DOWN).
- **Secondary finding — confirm resolved:** the prior gap where a fast-exiting fake
  platform could **wedge the daemon into a permanent BLOCKED state** should be cleared
  by the same restart path — confirm the wedge no longer occurs under the live fix.
- **Evidence:** test-mode harness `scripts/testmode/tc-layered-antitamper.sh`.
- **Honest limit (once closed):** friction + fast restore, not a seal — root can
  re-tamper / race the restore in a tight loop; covers **signed platform binary**
  authenticity.

### TC-34 🔴 Tampered bundled plugin binary should auto-revert to the genuine embedded binary before it runs — *FEATURE 23 (HF3) / Layered supervision (platform→plugins), **VITAL anti-tamper***  `[found 2026-07-14 — test-mode PASS 2026-07-14; live e2e pending for final sign-off]`
- **Origin:** a plugin binary swapped with a dummy **persisted and ran** (false-green
  enforcement bypass, register §6 limitations 11 + 13c). The platform owns each plugin's
  authenticity: **verify-or-restore the genuine embedded binary before every run.**
- **Check (sandbox/test-mode first, THEN LIVE, redaction-safe):** tamper a **bundled**
  plugin binary (**emptied + `chmod +x`** / **swapped** / **deleted**) → on the next run
  the platform **refuses the tampered/absent binary** and **restores it from the genuine
  embedded copy** (the plugin ships inside the Ed25519-signed, daemon-verified platform)
  before the plugin runs; the dummy **cannot persist** across cycles.
- **Expect:** the dummy never enforces in place of the genuine plugin; the genuine
  embedded binary is restored + run; no false green while unrestored.
- **Status: 🟢 test-mode PASS (2026-07-14) — live e2e still owed for final sign-off.**
  All **three** tamper variants on a bundled plugin (**empty + `chmod +x`** / **fake** /
  **delete**) → the genuine binary is **restored BEFORE the run**, the fake **never
  ran**, a `plugin_tamper_repaired` event was recorded, all within **~1 schedule
  interval**. This **PROVES in test-mode** the HF3 verify-or-restore behaviour that
  **TC-27** defined and **TC-06 / FEATURE 15** first addressed. **NOT yet DONE:** live
  e2e on the real install is still owed before final sign-off (register §5
  release-acceptance — DONE = live-verified + working locally). **⚠️ Human still to
  reconcile the F15-vs-F23 framing** (new feature vs regression of F15) — a separate
  naming decision (register §6 limitation 13c), not resolved by this pass.
- **Evidence:** test-mode harness `scripts/testmode/tc-layered-antitamper.sh`.
- **Unbundled-plugin caveat (honest scope + empirical finding 2026-07-14):** HF3's
  verify-or-restore covers **bundled / embedded** plugins only. **Unbundled** helpers —
  **browser-monitor** and **freedom-protector** — do not ship inside the signed platform.
  The verifier confirmed **browser-monitor is anti-tamper-GAP in USER mode** (its fake
  ran); this is **closed in SYSTEM mode by the bundled-only allowlist** (root-only) —
  **verify live**. **freedom-protector** authenticity remains a separate, still-open gap.
- **Honest limit:** friction + fast restore, not a seal (root can re-tamper / race);
  **binary** authenticity only — plugin **config/policy** integrity stays iceboxed (TC-10).

### TC-35 🟠 Companion offline backup rail is present + VALID — *FEATURE 18*  `[found 2026-07-15 — live FAIL]`
- **Origin (live finding, v0.18.0):** the out-of-band companion rebuilds the daemon
  after a total teardown from a **signed offline backup copy** (the rail TC-16/TC-17
  proved). On the v0.18.0 live build the companion's own health field reads
  **`watchdog_copy_ok=false`** — the backup it would rebuild from is **INVALID/broken**.
  A recovery rail that looks present but can't actually rebuild is the project's
  signature latent-failure shape (a rail that reads present over a dead capability).
- **Check (redaction-safe boolean):** the companion's signed offline backup exists and
  **verifies** (`watchdog_copy_ok` true), so the TC-16/TC-17 offline rebuild path is
  actually armed — not just reported present.
- **Expect:** backup present + signature-valid. **Status: 🔴 FAIL — live (v0.18.0,
  2026-07-15).** `watchdog_copy_ok=false`; the F18 offline-rebuild rail is **at risk**
  until the backup is repaired/re-seeded. **Needs a bug ticket + fix.** TC-16/TC-17
  stay PASS for the *mechanism* verified earlier, but their precondition (a valid
  offline backup) is currently broken on the live build — re-run TC-16/TC-17 once the
  backup is valid again.

### TC-36 ⬜ Disguise blend-in — the install isn't a matching set / anomalous to an apps-folder survey — *FEATURE 26*  `[found 2026-07-15 — TO VERIFY, DEFINING]`
- **Origin:** even with the process table clean (TC-30 PASS), the on-disk folders share
  a common vendor-style prefix + **hex/UUID suffix** repeated across the daemon home,
  the platform working folder, and the binaries — a single `grep`/eye-pattern over the
  apps folder picks them all out as a matching set, and the hex suffix reads as "not a
  real app."
- **Check (redaction-safe, sandbox first then read-only live survey):** a casual
  `grep`/`ls` survey of the apps folder cannot flag the install's folders as a matching
  set or as anomalous; names read like ordinary word-based apps (no hex/UUID suffix);
  no shared token/prefix/suffix ties the install's folders together; the shape is not
  reused install-to-install.
- **Expect:** install blends in — not a matching set, not anomalous. **Status: ⬜ TO
  VERIFY** (FEATURE 26 is DEFINING / not yet scheduled). Extends TC-30 to the on-disk
  naming layer.

---

## Utility-tier test cases (OUTSIDE the platform mesh suite — different execution path)

> **These do NOT belong to the platform mesh regression suite above.** The
> mesh suite verifies the **enforced tier** (signed daemon/platform/plugins) and
> is walked in full on every live deploy. The cases here verify the **utility /
> fallback tier** (ADR-0021) — standalone user-mode helpers that run where the
> enforced stack can't. Different code, different execution path, different (ad-hoc,
> per-machine) run model. They are recorded here so the ownership invariant holds
> (every shipped feature → a TC) **without** implying the mesh run model applies.

### TC-U1 🟡 (utility tier) mac-browser-guard quits a browser on a blocklisted tab — *FEATURE 20 / ADR-0021*  `[added 2026-07-02]`
- **Tier:** utility / fallback (user-mode script), NOT the enforced mesh — see ADR-0021.
- **Criterion:** the user-mode guard detects **ALL** open browser tabs (including
  non-active / background tabs) and **quits the browser** when any tab is on a
  blocklisted host — entirely in user mode (no sudo/admin).
- **Repro:** open a blocklisted site in Chrome, then run the guard's one-shot scan
  (`utils/mac-browser-guard/browser_guard.py`) → the browser quits.
- **Key-moment excerpt (redaction-safe, from the live run):**
  `tabs detected: 7` → `MATCH -> would kill: Google Chrome ( www.rba.com.au )` →
  `killing: Google Chrome` → `Chrome is KILLED`.
  *(`rba.com.au` was a temporary test blocklist entry; the shipped blocklist is
  google / youtube / bilibili / etc.)*
- **Expect:** all open tabs seen (incl. non-active), browser quit on a blocklisted
  host. **Status: ✅ PASS (live-verified 2026-07-01, real Mac).**
- **Honest limit (ADR-0021):** browser-only, user-mode, removable — thin friction,
  no signing/tamper-resistance/commitment-gate. Not a mesh guarantee.

### TC-U2 ⬜ (utility tier) browser-monitor self-runs standalone when the platform isn't installed — *FEATURE 27*  `[found 2026-07-15 — TO VERIFY, DEFINING]`
- **Tier:** utility / best-effort (user-mode), NOT the enforced mesh — see ADR-0021.
- **Criterion:** with the platform **not** installed, the browser-monitor self-installs
  a **user-level** runner (user launchd + cron fallback), **keeps running across
  logout/restart**, self-restarts/heals on casual deletion, and still quits a browser
  on a blocklisted tab — entirely user-mode (no sudo/admin). With the platform present,
  it runs unchanged as the enforced plugin.
- **Expect:** standalone self-run + self-heal delivers browser coverage where the stack
  can't run. **Status: ⬜ TO VERIFY** (FEATURE 27 is DEFINING / needs the human gate on
  its relationship to FEATURE 20 + ADR-0021).

---

## Run Log
| Date | Deploy | Pass | Fail | Notes |
|------|--------|------|------|-------|
| 2026-06-20 | daemon-v0.5.5 (F14) | TC-02, TC-03 | TC-05 | argv leak fixed live; watchdog recovery found broken |
| 2026-06-22 | (live restore) | TC-01 | TC-05, TC-06, TC-07, TC-08 | kill-steam tamper found + hand-restored; F15 fix pending |
| 2026-06-22 | platform v0.16.0→v0.16.1 (F15) | TC-01, TC-02, TC-03, TC-06, TC-07, TC-08, TC-11 | TC-05 | F15 plugin-integrity live-verified: tamper auto-restored (~6s) + surfaced in status (`tampered → repaired Nx`); deploy verified; watchdog recovery (TC-05) still open |
| 2026-06-22 | platform v0.16.2 (F16) | TC-11, TC-12, TC-13 | TC-05 (deferred) | F16 whitebox logging live-verified: steady-state log clean (TC-12) + tamper logged as `WARN plugin tamper repaired` independent of status (TC-13); watchdog (TC-05) deferred per owner |
| 2026-06-22 | platform v0.16.3 (status KISS) | TC-07, TC-08, TC-11 | — | status = current-state: recovered tamper → `ok`/OVERALL HEALTHY (no `tampered` verdict); tamper only in log/events; watchdog manually restored (`present`, never degrades). TC-05 still deferred (FDA) |
| 2026-06-29 | daemon-v0.5.6 (F17) + platform v0.16.4 (kill-steam Dota2 fix, #71) | TC-14, TC-19 | TC-21 (new) | F17 live-verified: workdir-delete recovered via baked fallback (down ~28s `desired=none` → adopt v0.16.3 → up ~56s, no permanent BLOCK; TC-14, go-reviewer-confirmed); fresh install "retired 4 prior generation(s)" → 1 platform (TC-19). Final: single generation, 1 platform, desired=good=v0.16.4, OVERALL HEALTHY. First TC-14 attempt was a false-pass (split-on-space path truncation → `rm` missed) — caught by a `test -d` gate; methodology lesson recorded on TC-14. NEW peer-reviewed bug → TC-21 (FAIL): post-recovery generations with deleted binaries are invisible to install cleanup + orphan platforms accumulate (hygiene gap, protection stays HEALTHY) |
| 2026-06-29 | daemon-v0.5.7 (F19 + F17 convergence fix, #73) + platform v0.16.4 | TC-14 (re-confirmed under F19), TC-20 | TC-21 (still partial), TC-22 (new) | F19 live-verified: `ps` for the mesh marker + role flags returns **0** (role moved off argv into plist env); supervisor labels varied/non-clustering (TC-20 PASS). TC-14 re-confirmed under daemon-v0.5.7. F17 convergence fix (#73) retires dead-binary/zombie generations + ends orphan platforms — **convergence logic works live** (install "retired 2 → 1 platform"), but TC-21 stays PARTIAL: (a) stale workdir/state.db files left on disk, (b) status-flip → **TC-22 NEW** (run-history DB read/write concurrency → false OVERALL UNKNOWN; fix in progress), (c) manual cleanup helper not F19-env-aware |
| 2026-06-29 | daemon-v0.5.9 (F18 + orphan-sweep, #76/#78/#80) + platform v0.16.7 (status snapshot, #79) | TC-16, TC-17, TC-21, TC-22 | TC-23 (new) | **F18 shipped + live-verified (Phase 1):** removed the entire in-band rail (daemon down, 0 platforms, 0 mesh plists) → the separate launchd companion (own folder, no FDA) detected the stale heartbeat and rebuilt the daemon from its signed offline backup (7.9MB, signature-checked) → mesh + platform back; companion excluded from mesh discovery/cleanup by construction (TC-16/TC-17 PASS; Phase 2 offline-platform restore deferred). **TC-21 PASS** — orphan-sweep (daemon-v0.5.8) verified live: after recovery/install churn exactly 1 state.db + 1 platform. **TC-22 PASS** — status snapshot (platform v0.16.7): 8 rapid reads stable, no false UNKNOWN; root cause was the status read contending with the reconcile writer, fixed by reading an atomic snapshot (3 prior attempts — orphan-sweep, WAL, rollback-journal — were dead-ends). **NEW TC-23 (FAIL):** the companion/watchdog rebuild path re-introduces the `--mesh` argv leak (F19 hides it only on the `daemon install` path) — `ps` shows `run --r a/b --mesh` after an out-of-band recovery until the next clean install; hygiene/friction, not a bypass |
| 2026-07-01 | mac-browser-guard (FEATURE 20, utility tier — NOT a mesh deploy) | TC-U1 | — | Utility-tier live check on a real Mac (outside the platform mesh suite): user-mode guard saw all 7 open tabs incl. non-active, matched a blocklisted host, and quit Chrome (`tabs detected: 7` → `MATCH -> would kill … www.rba.com.au` → `Chrome is KILLED`). PASS. |
| 2026-07-14 | HF1 / FEATURE 21 storage separation (branch `hardening/hf1-storage-separation`, fix `f0593fb`) — **SANDBOX / test-mode tier, NOT the real install, NOT deployed** | TC-25 (test-mode tier) | — | **TC-25 PASS at the sandbox/test-mode tier** — baseline-then-fix, both weaknesses reproduced OPEN on master + CLOSED on the fix, twice each, clean isolation. 25a: master baseline deleting the platform folder KILLS the daemon (co-located binary) → fix daemon SURVIVES + re-establishes the platform at a FRESH path (pids `67149→67339`, P1≠P0, daemon binary survived) [CLOSED]. 25b sibling roots (neither nested) PASS. 25c CRITICAL blast-radius: decoy real-install dir under `~/Library` DELETED on master by `install`, SURVIVES on the fix (sweep scoped to explicit support root) PASS. 25d no-regression (1 supervisor, mesh mutual-respawn) PASS. 25e status truthful (down honestly, no green-over-dead) PASS. Reusable script `scripts/testmode/TC-25-storage-separation.sh` (safe_rm-gated). **Honesty caveat:** test mode doesn't auto-relocate the binary → baseline EMULATED the prod single-root shared-fate; **live must confirm the real relocate.** **NOT yet live-verified** — 2 live-tier follow-ups: (1) a real disguised generation-workdir under the actual `~/Library` survives a REAL install; (2) status stays truthful when a genuinely-running platform's workdir is deleted. FEATURE 21 stays BUILDING (test-mode-tier PASS), NOT shipped. |
| 2026-06-29 | daemon-v0.5.10 + platform v0.16.7 (independent e2e re-verification) | TC-14, TC-16, TC-17, TC-20, TC-21, TC-22 | TC-23 (confirmed, #83); TC-24 (WATCH, noted) | **Independent e2e-verifier re-ran the suite on daemon-v0.5.10.** **TC-14 PASS** — status `desired=none` (platform down, no BLOCK) → `desired=v0.16.3` (baked fallback) → `platform running` (old platform pid dead, fresh state.db); held stable on the fallback ~12 min, no permanent BLOCK. **TC-16/17 PASS** — entire in-band rail removed (0 mesh plists, 0 platforms, gen-roots deleted) → the preserved companion rebuilt the daemon **UNATTENDED** into a fresh single generation (3 plists, 1 state.db, 3/3 roles, platform back). **TC-20 PASS** — `ps … grep 'run --r\| --mesh'` = 0; 3/3 roles. **TC-21 PASS** — 1 state.db, 1 pid, no zombies. **TC-22 PASS** — 10/10 rapid reads identical, 0 changes. **TC-23 stays FAIL — now confirmed with DECISIVE evidence (#83):** direct plist read after the companion rebuild → mesh plists with `--mesh` = **2**, F19 env marker = **0** (rebuild writes pre-F19-format plists); ruled out test-mode **and** stale backup; root cause not yet pinned (both prior theories refuted); hygiene/friction, clean install clears it. **NEW TC-24 (WATCH):** churn-window status-vs-disk skew — `status` printed "3/3 roles running" while on-disk generations = 0 for one sample, self-corrected next sample (latent-failure class; FAIL if reproduced). |
| 2026-07-14 | HF4 / FEATURE 24 disguise plugin & process identity (branch `hardening/hf4-disguise`, commit `f5a157c` on the HF1 base) — **SANDBOX / test-mode tier, NOT the real install, NOT deployed** | TC-28 (test-mode tier) | — | **TC-28 PASS at the sandbox/test-mode tier** — zero-leak greppability checks (criteria 1–3) come up **empty**: a sweep of the running system for the enforcement keywords, a version string, "focusd"/"platform", plugin names, and the working-folder location finds **nothing** that maps to the install. Platform process shows a generic name; plugin config carried off the command line; on-disk plugin folders + logs use neutral names; mesh labels no longer cluster; enforcement unchanged. **Honest limit:** casual-grade only — `argv[0]` (the binary path) stays root-visible via low-level APIs; goal is only that nothing *greppable* maps to the install in a `ps`/`grep` sweep. **NOT live-verified, NOT deployed** — a live e2e pass on the real install + a signed deploy remain (hardening-epic consolidation; note the F25 coupling: its orphan-reaper must be updated to recognise the disguised platform identity). FEATURE 24 stays BUILDING (test-mode-tier PASS), NOT shipped. |
| 2026-07-14 | Anti-tamper layered-supervision verifier — **TEST-MODE only** (harness `scripts/testmode/tc-layered-antitamper.sh`) | TC-34 (test-mode) | TC-33 (test-mode, empirical) | **TC-34 test-mode PASS** — all 3 bundled-plugin tamper variants (empty+chmod+x / fake / delete) restore the genuine binary **BEFORE run**, the fake **never runs**, a `plugin_tamper_repaired` event is recorded, within ~1 schedule interval (HF3 verify-or-restore PROVEN in test-mode; **live e2e still owed** for final sign-off). **TC-33 test-mode FAIL (empirical)** — a **fake** platform binary was **exec'd UNVERIFIED** (arbitrary code ran as the platform); an **empty** swap took the platform **DOWN indefinitely**; root cause = executor verify gated on **binary-presence**, no sig-check on the exec path; **verify-before-exec fix IN PROGRESS**. Secondary findings: (1) a **fast-exiting fake platform can wedge the daemon into a permanent BLOCKED state** (needs a process restart) — fixed alongside; (2) unbundled **browser-monitor** anti-tamper-GAP in **USER** mode (its fake ran), **closed in SYSTEM mode** by the bundled-only allowlist (verify live). **TC-33 stays OPEN until the fix re-tests PASS.** |
| 2026-07-15 | **v0.18.0 consolidated hardening build (HF1/HF3/HF4 + F25) — LIVE deploy, live-verified** | TC-30, TC-32, TC-33 | TC-31, TC-35 | **First LIVE verification of the consolidated hardening build.** **TC-30 PASS (live, VITAL)** — the workdir appears in **neither argv nor env**; the `ps -E` env-dump form no longer prints a `--workdir` env var → the exploit's grep→workdir→`rm` chain finds nothing. **FEATURE 24 VITAL invisibility bar MET live.** **TC-32 PASS (live)** — upgrade end-state: exactly **one** fresh disguised instance, zero old/duplicate/orphan, it **respawns** when killed (single-instance held), no leftover pre-disguise `--workdir` line; the F25 reaper recognised the disguised identity. **TC-33 PASS (live, restart path)** — verify-before-exec landed: a tampered platform binary is no longer exec'd unverified; the genuine **signed** platform is restored + runs (via the restart path). Also confirmed live: **signed release verified + exactly one generation** (single daemon + single platform). **TC-31 FAIL (live, VITAL recovery)** — a wiped working folder self-heals **only on the next platform restart**, NOT proactively → **FEATURE 21 stays BUILDING, not shipped**; needs proactive folder-delete self-heal. **TC-35 FAIL (live)** — companion offline backup rail reads **`watchdog_copy_ok=false`** → the F18 backup the companion rebuilds from is **broken** (recovery-rail-down; needs a bug ticket + fix; TC-16/17's precondition currently unmet on live). **Observation (not a TC flip): net-block DISABLED** — baked default with **no override present**, so packet-filter blocking is off, and **config/policy integrity is ABSENT** (no protection of the config itself — the iceboxed TC-10 gap; ties to the config→server direction, register §9). |
