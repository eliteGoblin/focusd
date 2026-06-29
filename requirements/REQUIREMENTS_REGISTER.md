# Focusd — Requirements Register

> **A BA-style master index for the focusd commitment device.**
> Captures *why* each feature exists, *what* it defends against, *what it doesn't*, and *where to go for deeper detail*. The unusual thing about this register vs a typical one: **the user is also the threat actor**, so the threat model + honest-limitations sections are first-class.

## How to read this

- **Want context fast?** Read §1 (mission), §2 (personas), §3 (threat model). That's the *why*.
- **Want the feature list?** §4 is the register — features with motivation, acceptance, status, and the honest limitations of each.
- **Want to know how the design choices fit together?** §5 (cross-cutting principles) — incl. the cross-platform-Go / interface-at-the-OS-seam engineering principle.
- **Want to know what's NOT defended?** §6 — the honest holes.
- **Coming back after a long break?** §10 (glossary) + §9 (open follow-ups) catch you up fast.
- **Need deep code-level detail?** This doc is the index; deep dives live at:
  - `app_mon/documents/design/daemon_design.md` — daemon mesh + self-protection
  - `requirements/app_mon/*.md` — per-feature historical requirements
  - Git commit messages — every shipped feature has a detailed body
  - PR descriptions on GitHub — review history + acceptance test plans

---

## 1. Mission

Defeat the user's gaming/internet addiction (specifically Steam + Dota 2) via a *personal commitment device*. The user installs focusd in a calm, deliberate moment; future-weak-user finds it hard to defeat.

Software cannot make a determined person stop. Software CAN make it sufficiently inconvenient that an *impulsive* attempt fails — buying enough time for the impulse to pass. That is the whole design philosophy.

---

## 2. Personas

| Persona | Description | Role |
|---|---|---|
| **User-as-developer** | Calm, deliberate version of the user, currently maintaining the codebase | Designs and ships the defenses |
| **User-as-future-weak-self** | Same human, in a moment of impulse, trying to defeat the defenses | The adversary the system is designed against |
| **Claude-as-tool** | Claude Code helping develop/maintain focusd | Aligned with user-as-developer |
| **Claude-as-threat** | Same Claude, asked by user-as-weak-self to bypass focusd | The most capable adversary, because Claude can read the source |

The two-faced nature of the user is the key insight. Most security designs assume the developer and the attacker are different people; here they are the same person at different times, and the developer's job is to constrain their own future self.

---

## 3. Threat model

**In scope (the system tries to defend against):**
- User-as-weak-self running `launchctl bootout`, `pfctl -F`, `rm -rf ~/.claude/skills/focusd-protection`, etc. directly from a shell.
- User-as-weak-self asking Claude "help me disable focusd" or any wrapper of that.
- User-as-weak-self asking Claude indirect questions ("is the daemon running?", "help me debug it") whose answers leak the strings needed for a direct bypass.
- Casual `grep` / `strings` attacks against on-disk binaries.

**Out of scope (the system explicitly does not defend against):**
- User-as-developer deliberately removing focusd in a planned, considered moment. The 5-gated override path is the supported channel for that.
- An external attacker with code execution on the machine.
- A non-Claude LLM agent. (Possible but not in our scope.)
- Apple changing AMFI semantics enough to break path-rotation. Documented honestly; would require redesign.
- Hardware destruction.

**What "defended" means:** raises the cost of bypass to a level the impulsive self won't pay. *Not* mathematically impossible. The whole design is friction-grade.

---

## 4. Feature register

| # | Feature | Motivation | Status | Honest limitations |
|---|---|---|---|---|
| **Daemon mesh** | 3 launchd jobs (a/b/ensurer) monitor each other so killing one respawns it | Defeats `kill -9 PID` of a single process | ✅ shipped | sudo can bootout all 3; requires knowing labels |
| **Disguised paths/labels** | Mesh uses random plausible-looking names (e.g. `com.brave.Browser.helper.*.xxx`) | Defeats `launchctl print system \| grep focusd` | ✅ shipped (FEATURE 2: pool widened from ~2M to ~5e15 combos) | Useless once attacker reads `relocate.go`; useless when other tells (hidden-dot workdir, --mesh flag, workdir-layout fingerprint, pubkey verification) still leak |
| **Path-rotating self-update** | New daemon binary placed at fresh disguised path → AMFI accepts | Allows daemon-code updates on existing installs without uninstall/reinstall | ✅ shipped (FEATURE 1.5) | AMFI premise verified empirically on Frank's Mac; Apple could change this |
| **Pubkey grep-resistance** | XOR-mask the embedded Ed25519 pubkey at build time | `strings daemon \| grep "BEGIN PUBLIC"` returns 0 | ✅ shipped (FEATURE 3) | Disassembler still extracts the key; friction not crypto |
| **Ed25519 signed releases** | All daemon + platform binaries Ed25519-signed; daemon verifies before exec | Prevents fake-release injection that the daemon would run | ✅ shipped | Private key still on dev's machine (`~/.creds/`); should move to CI-only |
| **3-step uninstall ritual** | `daemon uninstall` requires transcribing 3 long passages with 2h + 4h waits | Defeats impulsive uninstall (~6h real-time delay) | ✅ shipped | User who knows the passages can paste them; the wait, not the typing, is the defense |
| **Skill-protector plugin** | Re-injects `~/.claude/skills/focusd-protection/SKILL.md` + always-on rule + SessionStart hook | Stops the bypass where user `rm`s the skill then asks Claude to disable | ✅ shipped (FEATURE 1) | Periodic re-injection (every 5m) now fires on schedule via FEATURE 8's runtime privilege-drop (system platform steps down to the console user, `run_as: current_user`). Resolved #37 |
| **5-gated override path** | Claude refuses bypass unless: GH issue ≥24h old, signed override commit, today's BBC SHA256, hand-typed 40-word passphrase | Defeats Claude-mediated bypass requests | ✅ shipped (in SKILL.md) | Honor-system on "typed by hand"; clock-manipulation partly mitigated by gate 4 |
| **Skill redaction rule** | Skill instructs Claude to refuse showing disguised paths/labels/binary-names, refuse discovery commands (ps/launchctl/find/log-tail) | Closes "Claude leaks the strings while helpfully answering an indirect question" path | ✅ shipped (FEATURE 5) | Claude must voluntarily follow its own skill — behavioral only |
| **dns-block plugin** | Writes `0.0.0.0 steampowered.com` etc. to /etc/hosts | First-line block (DNS resolution fails) | ✅ shipped (pre-session) | Bypassed by custom resolver / DoH / hardcoded IPs in client app |
| **kill-steam plugin** | If Steam.app process found, kill it and remove `/Applications/Steam.app` | Direct process kill + on-disk removal | ✅ shipped (pre-session) | User can reinstall; the loop catches that within 10s |
| **network-block plugin** | Reconciles `pfctl` table with DoH-resolved Steam IPs every 30m | Defense-in-depth: direct-IP traffic (bypassing DNS) caught at kernel packet filter | ✅ shipped (FEATURE 4); disabled by default, enabled via override config | Manual prereqs (pf anchor + /etc/pf.conf entry); IPs rotate; reconciler keeps it current |
| **freedom-protector plugin** | Keeps the third-party Freedom focus app + its FreedomProxy helper alive: each ~10s pass relaunches whichever was shut down (inverse of kill-steam — *defends* an app instead of killing one) | Closes the easiest weak-moment bypass — quitting the blocking app (Cmd-Q, no terminal/sudo); Freedom comes back within seconds | ✅ shipped (FEATURE 11, PR #51); skips cleanly when Freedom isn't installed | Login-item re-enable is best-effort/manual-verify (no public scriptable setter — mirrors FEATURE 10); keeps it running but doesn't stop a user who removes the app or disables blocking from inside Freedom; macOS-only; user must install/license Freedom |
| **Daemon bug fixes** | Bug 1 (config staleness), Bug 2 (atomic install + rollback), Bug 3 (no auto-resolve from reconcile loop) | Foundational reliability fixes | ✅ shipped (v0.10.0 + daemon-v0.1.0) | None outstanding |
| **`daemon status` health snapshot** | Read-only health read that NEVER leaks disguised tokens — closes the "indirect question whose answer is a bypass recipe" path. `daemon status` reports only daemon-owned facts (mesh roles up / platform process / version) and **delegates** plugin/protection detail to a new `platform status`, so the daemon stays plugin-agnostic (KISS) | ✅ shipped (FEATURE 9, #45; redaction structural per ADR-0011; KISS layering per ADR-0012) | Status is a read, not a protection; per-protection recency is a last-run-status proxy (not a live re-probe) per ADR-0012; mesh/admin lines read "unknown" without sudo; age buckets coarse by design |
| **Platform singleton enforcement (daemon-held flock)** | Both mesh roles were independently starting a platform → two platforms on one healthy install (double plugin runs + DB contention; surfaced by `daemon status`). A crash-safe OS advisory lock held by the **daemon** lets exactly one daemon supervise the single platform; the loser starts nothing | ✅ shipped (#48; daemon-layer only; decision per ADR-0013) | Only macOS double-launches today (mesh is macOS-only); Windows/Linux carry the lock for future-readiness with no mesh yet to dedup |
| **Mesh label decorrelation + faster self-heal** | The 3 mesh entries shared one random base + role suffix (`.a`/`.b`/`.ensure`) — find one, grep the prefix, all three fall out; and the suffix advertised the backstop. Each role now gets an independent random label (no shared base, no role tell), the roster is persisted as a XOR-masked workdir file (reusing FEATURE 3's mask), and the heal loop tightens ~10s → ~2s so single-entry manual removal loses the race. Closes #20 | ✅ shipped (FEATURE 10, #50; decision per ADR-0014) | Casual-grade friction only: defeats `cat`/`ls` + the `grep <prefix>` cluster-find + slow manual removal; does NOT stop reading the binary to recover the XOR key, the daemon's own un-mask path, or a scripted atomic bootout+rm of all three |
| **Out-of-band watchdog** | The in-band mesh heals every *partial* attack in ≤2s (verified live, incl. a 241-pass kill-storm), but a **total atomic teardown** (delete all 3 plists + kill all processes + wipe workdir) leaves no survivor — the ensurer is itself in-band, so nothing rebuilds. A watchdog on a **separate rail (cron)** runs the **same daemon binary** as a second copy outside the workdir; once a minute it checks for a healthy mesh and, if absent, runs the existing install path with fresh labels — **locally, no network fetch** (avoids the ADR-0015 fetch-storm). `daemon install` becomes one-shot (mesh + watchdog copy + cron); the two rails **mutually re-install** each other; self-update keeps the copy in sync | ✅ shipped (FEATURE 12, **PR #52**; decision per ADR-0016). **Verified live:** one-command `daemon install -v <ver>` stood up mesh 3/3 + platform v0.15.0 + watchdog rail (acceptance #3/#4); `daemon status` reports "out-of-band watchdog: present" without naming cron; **battle-test** — a total teardown (3 plists + all processes + workdir wiped → fully down) was rebuilt in ~5s (worst case ~60s), back 3/3, OVERALL HEALTHY (acceptance #1). go-reviewer + security-reviewer; 2 HIGH fixed pre-merge (empty-version broken-cron guard; Ed25519-verify the watchdog copy before any root reinstall) | Friction, not a seal (all held in the live test): cron is fragile on modern macOS (Apple deprecating it; may need Full Disk Access) so the watchdog's own health must be checkable — now shown as a status line; ~1-min recovery window is enough to launch a game; STILL LOCAL — a sudo user can wipe both rails atomically and repeat; resurrection-vs-uninstall footgun (accepted; uninstall best-effort-removes the rail); adds complexity to the delicate two-copy self-update. Durable weight stays in the server-side override gate |
| **Heartbeat + accountability-partner alerting** | The external/social layer. Every *local* layer (mesh + out-of-band watchdog) is friction a sudo user can eventually wipe; a server can't restart a wiped client, but it **can notice protection went down and tell a human**. The platform emits a periodic **authenticated heartbeat** (likely a plugin); the server tracks each device up/down + last-seen; if heartbeats stop past a threshold it flags the device **DOWN** and **alerts the accountability partner**, who has an app/dashboard of all linked devices. **Dead-man semantics:** absence of a signed heartbeat IS the down signal, so firewalling the channel triggers the alert anyway, and a wiped client can't forge "I'm fine." Product owner: "very important for addicted people" | 💡 proposed (FEATURE 13; **icebox / not scheduled**). The detection/alerting half of the server-managed-enforcement icebox entry. **Needs human sign-off** (new server, new persona, privacy questions) before any build | Detects + alerts; does **NOT** restart the client (server can't reach a dead agent). Needs real infra (server, device enrollment + auth, partner app). Deterrent is **social** — worthless if the partner ignores alerts. There's a few-minutes-of-freedom window before an alert lands; local friction still carries those minutes. Privacy (status-only vs log excerpts) + partner consent are **open product questions** |
| **Plugin-binary integrity reconcile** | Live finding (most severe to date): genuine plugin programs are unpacked to disk **once at platform startup**, then the schedule runs **whatever program is on disk** — never re-checked. The weak-moment owner (root here) overwrote the Steam-killer with a do-nothing stand-in → Steam/Dota ran freely, the dead stand-in exited cleanly, and `status` still read **"ok"** (false green over dead protection). A **direct enforcement bypass of the whole plugin layer** + the project's signature latent-failure shape. Fix (no new crypto): the genuine plugin copy already travels *inside* the Ed25519-signed, daemon-verified platform binary, so it's already the trusted golden reference. Make each plugin program **continuously-enforced signed desired state** — verify on **every** reconcile tick (and at point of use), restore the genuine program atomically on mismatch, and record the mismatch as a **tamper event** (recorded as a log line + platform event — audit; `status` reflects current state, not tamper history). Also fixes the twin truthfulness bug: an intentionally-disabled plugin must not read **DEGRADED** | ✅ shipped (FEATURE 15, platform **v0.16.0** + **v0.16.1**; PRs #62/#63; **ADR-0019**). **Live-verified 2026-06-22:** tampered plugin auto-restored to genuine in ~6s (TC-06); false-green killed by restore-before-run; disabled net-block no longer false-DEGRADED (TC-08). **Refined 2026-06-22:** `status` is current-state only — a since-restored plugin reads `ok`, the tamper history goes to log/events, not a persistent status verdict (TC-07 pending re-verify) | **Friction + fast self-heal (≤1 tick) + detection — not a seal:** root can re-tamper / race the restore; a root symlink-swap of the plugin dir can redirect the (root) atomic-restore write (pre-existing residual of the root context, unchanged from ExtractTo — security-review HIGH, accepted, not a regression). Covers plugin **binary** integrity only — plugin **config/policy** integrity is a **separate** iceboxed follow-up (TC-10). The strategic alternative (link plugins *into* the signed platform binary to remove the on-disk surface) is a deferred DESIGN gate |
| **Whitebox action logging** | The F15/TC-07 false-green showed that relying on a single observability path (`status`) is a single point of failure — a tamper event was recorded to the DB but a status-rendering gap read `ok` anyway. The **app log is an independent, append-only channel**: every significant engine action is now written as a structured, leveled, **redaction-safe** line (tamper → WARN; integrity-check/sweep failure → ERROR with the error *class* only; normal lifecycle → INFO), and a **clean steady-state run emits no WARN/ERROR** so the e2e suite can verify the log whitebox. | ✅ **shipped** (FEATURE 16, **PR #65**, platform **v0.16.2**; extends the "Observability is non-negotiable" principle, no new ADR). **Live-verified 2026-06-22:** steady-state log clean — 0 ERROR/WARN (TC-12); a tamper logs `WARN plugin tamper repaired plugin=… want_sha=… got_sha=…` with **plugin id + sha prefixes only**, present in the log **independently of `status`** (TC-13). | The log lives under the disguised workdir (redaction applies when e2e reads it); a **root adversary can edit/delete the local log** — append-only is best-effort on-box, so the log is audit + an e2e channel, **not a tamper-proof record** (a tamper-proof stream needs the off-box heartbeat layer, FEATURE 13 / icebox). "All actions" = *significant* actions, not a debug firehose. |
| **Daemon recovery resilience** | Live incident (2026-06-29): the owner deleted the platform **workdir** + toggled off the Login Item and protection went **fully down with no auto-recovery** — games ran. Two root causes: (a) the desired platform version lived **only** in the workdir state, so a wiped workdir made the daemon log `BLOCKED: no desired version` and **never re-fetch** (permanent BLOCK, not recovery); (b) repeated installs/recoveries **piled up stale generations** (live: 6 workdirs + 14 disguised supervisor entries), breaking the per-workdir singleton (2 platform procs) and littering tells. The old "daemon recovers platform" test only deleted the **binary** — state survived — so the workdir-delete path was a **latent failure**. Fix: **bake a fallback platform version into the daemon binary** so a wiped workdir falls back + re-fetches + runs (no BLOCK); make **install idempotent with a global singleton** that cleans/supersedes prior generations (exactly one live generation) | ✅ **shipped + live-verified** (FEATURE 17, **daemon-v0.5.6**, 2026-06-29). **TC-14** (wiped-workdir recovery): live + go-reviewer-confirmed — platform down ~28s (`desired=none`) → adopts baked fallback (`v0.16.3`) → re-fetch + verify → back up ~56s, **no permanent BLOCK**. **TC-19** (single live generation): fresh install "retired 4 prior generation(s)" → exactly 1 platform. **One open follow-up → TC-21** (below) | Baked fallback can be older than the last desired (re-fetch rolls forward); root can delete faster than recovery — friction, not a seal; rolling forward past the baked version still needs a reachable fetch source. **Open follow-up (hygiene, not bypass):** after workdir-delete/recovery *cycles*, generations whose binary was deleted are **invisible** to install-cleanup (verify-gated discovery skips them) and orphan platforms accumulate → drifts back above one generation; protection stays HEALTHY (more enforcers, not fewer). Fix: treat deleted-binary entries as dead generations + retire them. e2e TC-21 (FAIL until shipped) |
| **Resilient out-of-band watchdog (companion)** | The FEATURE 12 cron rail failed live exactly when needed: modifying cron on modern macOS needs **Full Disk Access** the daemon/automated contexts lack, so the companion could **neither self-heal nor be scripted-restored** — it sat DOWN. And recovery was network-bound. Fix: replace the **cron rail with a launchd-based out-of-band agent the daemon can create/repair without FDA**, and keep a **signed offline backup of the platform binary** in the companion's own folder so it restores the engine **offline** (no fetch) after a workdir/binary wipe; signature-verified before promotion | 🔨 defining / approved-building (FEATURE 18, owner-approved 2026-06-29; **ADR-0020 reverses ADR-0016's cron choice**). **Supersedes the deferred TC-05**; maps to TC-16/TC-17/TC-18 | Determined root can still remove both rails — friction, not a seal (durable lock = off-box layer, FEATURE 13 / icebox); offline copy may lag latest desired (restores first, rolls forward later) |
| **Deeper disguise** | The owner spotted the install at a glance during the incident: `ps aux \| grep mesh` revealed the mesh role/marker; the Login-Items entries read as **three near-identical** `com.vendor.X.<hex>` siblings ("3 look very similar, obvious"); and **many** stale records from old generations were visible. Fix: **hide the mesh role/marker from the process list** (grep-for-mesh finds nothing), use **varied plausible single/2-word names that don't cluster** instead of the hex triplet, and **clean up stale login-items/background-task records** from prior generations | 🔨 defining / approved-building (FEATURE 19, owner-approved 2026-06-29; extends FEATURE 14). Maps to TC-20 | `argv[0]` (binary path) stays visible to root — friction, not invisibility; disguise never resists reading the source (register §5) |
| **Mesh argv leak minimization** | Live finding: `ps` shows every mesh process's full command line to root, and it bakes in the workdir path, the GitHub channel (a focusd-identity tell), the platform asset, the heal interval, a test-mode flag, and — worst — the **full list of all three roster labels in clear text** (the exact `launchctl bootout` keys). That one-line leak silently defeats FEATURE 10's on-disk decorrelation: the masked roster file was added so the three labels couldn't be grepped as a cluster, but argv printed them anyway. This feature strips the command line down to **role + mesh marker only**; the masked on-disk roster (FEATURE 10) becomes the single source of truth for the labels, and the channel/asset are compiled-in/derived (ADR-0017 spirit), not passed in plain sight | ✅ **shipped** (FEATURE 14, **PR #60**; decision per ADR-0018). Refines/partly-reverses FEATURE 10/ADR-0014's reliance on argv carrying the roster. **Verification split (honest):** *live-verified* on a real launchd test-mesh — `ps` carries none of the 3 roster labels / channel / asset / interval, and self-heal (bootout/kill/disable) at parity-or-better with master; *unit/integration-only* — the prod-truly-minimal argv (no workdir flag), the masked-file roster read (test mode computes labels, so the live mesh didn't exercise the file read), and the old/new/mixed-plist migration matrix; *not yet done* — live PROD mesh + live self-update migration (gated on the product owner's deploy). Post-review hardening: both masked-roster read sites now reject any roster that isn't exactly the 3 expected labels (truncated/edited file → fall back, not coordinate on garbage) | **`argv[0]` is always visible** — the binary's own path is shown by `ps` to root on macOS/Linux and the OS can't hide it; since the disguised binary lives in the workdir, the **workdir/binary path stays discoverable** no matter what. This removes the three bootout keys + the focusd-identity tell from `ps`; it does NOT make the process invisible. Friction, not a seal. Separately, path-rotating self-update accumulates **stale Login-Items / background-task records** (visual tell + clutter; live protection doesn't depend on them) — cleanup is iceboxed, not committed here |

---

## 5. Cross-cutting design principles

### Self-recovery is non-negotiable
The whole system is designed to be reliable and self-healing: **every protection
layer must recover without manual intervention after any single failure.** A
status that reads "healthy" but secretly depends on a hand-placed or leftover
artifact is a **latent failure, not health** — the moment that artifact is gone,
protection is gone, with no green warning. "Recovered" must mean the system
re-created what it needed *on its own* (re-fetched, re-installed, re-started),
end-to-end. Therefore verification must exercise the **real failure-and-recover
path**, not a surface-green check: tear the thing down for real and confirm it
comes back by itself. (See §6 limitation 10 for the live lesson that motivated
making this a named principle.)

### KISS — streamline, don't be "too smart"
KISS is a core philosophy, not a nicety. Prefer the **simplest correct**
solution; avoid over-clever, over-configurable designs that make the product
complex and create footguns. Concretely: **prefer deriving a deterministic value
over exposing an operator-supplied knob.** Speculative flexibility that wasn't
required is a defect surface, not a feature — every knob is one more thing that
can be set wrong, drift between code paths, and fail silently. When a value can
only ever be one correct thing, compute it once; don't let anyone configure it.
(Captured as a decision in ADR-0017.)

### Observability is non-negotiable
Every component must be **observable through captured, persisted logs** (plus any
metrics/telemetry it has). If a component's stdio can't be captured by its
supervisor, the component must **write its own log entries** — logging is not
optional. A component that logs to `/dev/null` is a defect: it hides failures,
and a "healthy"-looking system whose logs you never read is the same *latent
failure* the self-recovery principle warns about. This is why verification is
**white-box** (see `e2e-verification.md` V7/V8): the verifier reads the logs,
metrics, and automated-test results — not just the externally-visible behaviour —
and treats an unexplained `ERROR`/`WARN`, a missing log, or a missing automated
test as a finding to raise with the dev team. (Live lesson: the ADR-0017 self-heal
bug stayed invisible for so long because the engine's logs were being discarded.)

### Status = current state; history = log + events — never mix them
`status` is a **current-health** read and must stay clean (KISS): it answers "is
this protection genuine and working *right now*?", not "was it ever tampered?".
A plugin that was tampered and has since auto-restored is genuine and enforcing
now, so it reads **ok** — that is the honest present state. A currently-broken /
unrestored tamper or a real run-error reads **not-ok**. The "was there a tamper
attempt?" question is answered by the **audit channel** — the app log (FEATURE 16)
and platform events in the state DB — readable by a future accountability/dashboard
view (FEATURE 13 / icebox). Truthfulness about the present (no false-green, no
false-degraded) comes from the **restore mechanism and live signals**, not from
holding historical verdicts in status. (Live lesson, 2026-06-22: a persistent
"tampered → repaired Nx" verdict held in status for 24h made a recovered, genuine
plugin look unhealthy and dragged OVERALL down — history leaking into current
state. See FEATURE 15, ADR-0019 refinement, TC-07.)

### Friction, not cryptography
None of the defenses are mathematically unbreakable. Every one is a delay or an inconvenience. The bet: an impulsive person will give up before paying the cost; a calm person who genuinely wants out has the 5-gated override.

### Prose-level defenses are stronger than obscurity (against Claude specifically)
Disguise/pool/mask defeats `grep`. It does *not* defeat Claude-reading-the-source. The defenses that *do* resist Claude are the ones rooted in:
- **Time** (gates 1+2 — 24h wait — Claude can't make time pass)
- **Real-world state** (gate 4 — today's BBC News — Claude can't pre-compute tomorrow)
- **Behavioral commitment** (skill rules — Claude follows its own instructions)
- **Off-machine trust roots** (GH releases, GH issues — Claude can read but can't fabricate)

### Layered enforcement
No single layer is sufficient. The model assumes any one layer might be defeated; the combination is what holds:
- DNS layer (dns-block) → catches casual domain access
- Kernel packet filter (network-block) → catches direct-IP traffic
- Process layer (kill-steam) → catches running app
- File layer (kill-steam) → catches on-disk reinstall
- Identity layer (Ed25519) → catches fake-release injection
- Behavioral layer (skill + redaction rule) → catches Claude-mediated bypass

### Cross-platform Go, interface at the OS seam
focusd's daemon and platform are written in Go and are meant to run on **three**
platforms: macOS, Windows, and Linux. macOS is the only real deployment today;
Windows and Linux are in the build matrix and are the future target. The standing rule:

- **Default to portable.** Write code that works across all three platforms.
- **Interface at the OS seam.** When an OS-level primitive is *not* portable,
  define a port (an interface) with one adapter per OS behind it — rather than
  scattering OS-specific calls inline throughout the code.
- **Prefer a vetted library over hand-rolled per-OS code** *when* the architect
  endorses it AND it preserves focusd's hard constraint of trivial, dependency-free
  cross-compilation (no native-toolchain build step).
- **KISS governs the call.** If the simplest correct path is to build the
  interface, build it; don't over-abstract, but don't bury non-portable calls
  inline either.

First worked example: the platform-singleton (single-instance lock) decision —
captured in **ADR-0013** (`decisions/0013-platform-singleton-daemon-flock.md`):
a daemon-held, crash-safe advisory lock expressed as one port with a per-OS
adapter, reusing the dependency already present.

### Honest caveats are first-class
Every commit message + every PR description includes an explicit "honest" or "honesty" section. Pretending defenses are stronger than they are weakens the user's calibration. Examples: "AMFI premise unverified until smoke test", "private key still on disk", "the disguise is theater against me specifically", "this is friction not crypto".

---

## 6. Honest limitations (the things that don't work as well as they appear)

1. **Private key still on Frank's Mac** (`~/.creds/focusd_ed25519_private.pem`). Should be removed; signing should move to GitHub Actions Secrets only (already plumbed in `daemon-release.yml`). Until then, anyone with shell access can sign fake releases.
2. **Source repo on Frank's Mac** means Claude can always read `relocate.go`, the XOR mask logic, the skill content, the embedded passages. Every "secrecy" defense crumbles against this. The strongest hardening is moving the repo off this machine.
3. ~~Skill-protector re-injection doesn't fire on schedule.~~ **RESOLVED (FEATURE 8).** The single engine runs the behavioural protection by briefly stepping down to the user's identity; deletion of the skill file is now auto-healed on schedule (verified green on hardware 2026-05-31).
4. **AMFI premise** (path-rotation defeats CDHash cache) verified empirically once. Apple could change this in a future macOS version; would require redesign.
5. **The 3-step uninstall ritual's passages are embedded source.** A user who reads the source can paste them. The defense is the multi-hour wait, not the typing per se.
6. **The "user-typed-it-back" exemption in the redaction rule** was removed after security review pointed out it's circular (if disguise works, user doesn't know the string; correcting a near-miss reveals it).
7. **Customized settings don't pick up new shipped defaults.** When focusd ships a new default for a feature you've customized via an on-disk override, your override keeps your old value — focusd won't silently overwrite your choice, but it also won't merge in the new default. You must update the override to adopt new defaults. (Caught in practice: a release shipped the full Steam domain list, but an install whose override still held the old short list kept blocking only the short list until the override was refreshed.)
8. **Single engine = shared fate.** If the one protection engine crashes, all protections pause for the few seconds it takes to auto-restart (the previous two-engine design failed independently). Accepted trade for simplicity — see ADR-0010.
9. **Console-user assumption.** The step-down-to-user mechanism assumes one primary person at the keyboard; during login-window / fast-user-switch it correctly waits rather than mis-writing, and a shared/multi-user Mac is out of scope.
10. **Latent self-heal gap: the protection engine couldn't actually re-fetch itself (RESOLVED — verified live, daemon-v0.5.3).** A serious flaw was found live: every rebuild path (first install, self-update, and the out-of-band watchdog) used the **wrong download identity for the protection-engine binary**, so the recovery download failed. It was masked for a long time because the binary had been placed by hand — so status read HEALTHY while auto-recovery was in fact broken (exactly the *latent failure* the self-recovery principle in §5 warns about). **Two root causes, both lessons:** (a) an over-configurable free-form input for a value that should always have been **derived** — directly the anti-pattern KISS / ADR-0017 now forbids; and (b) the same mistake **copied across every rebuild path**, so no single path was a safe fallback. **Fix (shipped daemon-v0.5.3):** derive the deterministic download identity once and remove the knob — every recovery path becomes correct by construction — shipped via the in-place self-update so protection never dropped. **Verified live:** engine self-heal (kill + delete binary → re-fetched in ~4s) and a full teardown (every entry + all processes + work directory wiped → out-of-band rail rebuilt + re-fetched the engine in ~45-60s), both with no manual placement (see `e2e-verification.md` V1/V2). Decision recorded in ADR-0017.
11. **Plugin-binary integrity was unenforced after startup → false-green enforcement bypass (RESOLVED — FEATURE 15 shipped platform v0.16.0/v0.16.1, live-verified 2026-06-22, ADR-0019).** Another — the **most severe** — instance of the project's signature latent-failure class. The genuine plugin programs were placed on disk **once at platform startup**; the schedule then ran **whatever program sat at that location**, never re-checking it. The weak-moment owner (root on this machine) overwrote the Steam-killer with a do-nothing stand-in: Steam/Dota ran freely, and because the dead stand-in exited cleanly, `status` reported the plugin **"ok"** — a green light over dead protection that would persist **until the next platform restart**. Unlike a leak or a head-start, this is a *direct enforcement bypass of the entire plugin layer*. **Fix (no new crypto):** the genuine plugin copy already ships *inside* the Ed25519-signed, daemon-verified platform binary, so it is already the trusted golden reference — promote plugin programs to **continuously-enforced signed desired state** (verify every reconcile tick + at point of use, atomic restore on mismatch, **tamper recorded as a security event** — a log line + platform event). False-green is closed by **restore-before-run**, not by a status flag. **Refined 2026-06-22 (KISS):** `status` reflects **current state only** — a since-restored plugin is genuine + enforcing now, so it reads `ok`; a currently-unrestored tamper or real run-error reads not-ok; the tamper **history** lives in the log/events (audit), never as a persistent status verdict. The twin truthfulness bug — an *intentionally-disabled* plugin counting as DEGRADED (false-degraded) — is fixed in the same feature. **Honest residual:** friction + fast self-heal (≤1 tick) + detection, not a seal (root can re-tamper/race); **binary** integrity only — plugin **config/policy** integrity is a separate iceboxed follow-up (TC-10). Live demonstration + regression: `e2e-test-history.md` TC-06/TC-07/TC-08.
12. **Protection went fully down and did NOT auto-recover — wiped-workdir BLOCK + Login-Item kill-switch + watchdog needs FDA + generation pileup (2026-06-29 incident; being addressed by FEATURE 17 + FEATURE 18 + FEATURE 19, approved-building).** The owner deleted the platform **workdir** and toggled off the Login Item; the daemon did **not** recover and games ran. **Four confirmed root causes:** (a) **wiped-workdir = permanent BLOCK** — the desired platform version lived **only** in the workdir state, so a wipe made the daemon log `BLOCKED: no desired version` and **never re-fetch** (→ FEATURE 17: bake a fallback version into the daemon so it falls back + re-fetches instead of blocking); (b) **Login-Item kill-switch** — toggling the Login Item off unloads the launchd mesh and **macOS gives no API to re-enable it**, so nothing in-band brings it back (→ FEATURE 18: a surviving out-of-band rail re-establishes the mesh); (c) **the out-of-band watchdog needs Full Disk Access** — the cron rail can't be written by the daemon/automated contexts (no FDA), so it neither self-heals nor scripts-restores (→ FEATURE 18 / ADR-0020: launchd rail without FDA + signed **offline** engine backup); (d) **generation pileup** — repeated installs/recoveries stacked **6 workdirs + 14 disguised supervisor entries**, breaking the per-workdir singleton (**2 platform processes** ran) and littering visible tells (→ FEATURE 17 idempotent install/global singleton + FEATURE 19 stale-record cleanup). **The latent-failure lesson (again):** the only "daemon recovers platform" test ever run deleted the **binary** — and the desired-version *state survived that delete*, so it passed — while the **workdir-delete** path was never tested and was broken in the wild. **Process fix:** the e2e suite now carries a full **live teardown matrix** (binary / workdir / Login-Item / kill-all + combinations) as permanent TCs, and recovery may **never** again be claimed from a single vector (`e2e-test-history.md` TC-14…TC-20; TC-17 supersedes the deferred TC-05). **Update 2026-06-29 — FEATURE 17 shipped + live-verified (daemon-v0.5.6):** root causes (a) wiped-workdir BLOCK and (d) generation pileup are **closed** for the steady/fresh-install case — wiped-workdir now recovers via the baked fallback with no permanent BLOCK (TC-14), and a fresh install converges to one platform/one generation (TC-19). **Residual follow-up (hygiene, not bypass):** after workdir-delete/recovery *cycles* (not steady state), generations whose binary was deleted are **invisible** to install-cleanup and orphan platforms accumulate, drifting back above one generation; protection stays HEALTHY. Tracked as e2e **TC-21** (FAIL until the F17 deleted-binary "dead generation" retire ships). Causes (b) Login-Item kill-switch and (c) watchdog-needs-FDA remain with FEATURE 18 (approved-building).

---

## 7. Versioning and release cadence

| Component | Tag prefix | Current latest | Notes |
|---|---|---|---|
| Platform | `v*` (e.g. `v0.16.4`) | v0.16.4 | Triggers `platform.yml` CI. v0.16.x line: plugin-integrity (F15), whitebox logging (F16), status KISS, kill-steam Dota2-each-run fix (#71, v0.16.4) |
| Daemon | `daemon-v*` (e.g. `daemon-v0.5.6`) | daemon-v0.5.6 | Triggers `daemon-release.yml`, signed in CI. Daemon status (FEATURE 9) + singleton flock (ADR-0013) + recovery resilience (FEATURE 17, daemon-v0.5.6) shipped in the daemon-v0.5.x line |
| Legacy app_mon | `appmon-v*` | (none since refactor) | Triggers legacy `release.yml`; scoped down in PR #39 so platform tags don't trigger it |

Platform releases ship bundled plugin binaries embedded via `//go:embed` (`platform/internal/bundle/data/<plugin>/<plugin>`). Bundled binaries are force-committed past `data/.gitignore` to ensure CI builds them in. **Hygiene gap (#38) — RESOLVED (PR #44):** `scripts/build-platform.sh` now auto-rebundles plugins before building, so the bundle staleness that caused the v0.12.0 → v0.12.1 hotfix can't recur.

---

## 8. Deploy mechanics (current state on Frank's Mac)

- Platform: deployed via daemon-managed pre-populate workaround (`<workdir>/bin/<version>/platform` + write desired version). Daemon swaps within ~10s.
- Daemon code: deployed via `sudo daemon self-update <daemon-tag>` which path-rotates to a new disguised location (FEATURE 1.5).
- Skill files: written into `~/.claude/` by the skill-protector plugin. Re-injection now runs on schedule (every ~5m) via FEATURE 8's privilege-drop — no longer a manual step (#37 resolved).
- **FEATURE 12 (out-of-band watchdog) deploy — honest mechanics.** F12 merged to master (PR #52) and was deployed live as a **locally-built SIGNED daemon** — **no formal `daemon-vX` release tag was cut this round** (so §7's daemon row is unchanged for this deploy). The platform **v0.15.0** binary was **placed manually** (the daemon-managed pre-populate workaround) because **at the time of the F12 deploy the ADR-0015 fetch-storm bug was still open**. That bug is now **RESOLVED (PR #54)** — a direct release-download URL replaced the rate-limited API call, so deploys no longer need the manual pre-populate workaround (see §9). One-command install + status + a live total-teardown battle-test were all verified against the original F12 deploy.
- **One-command system-mode install:** the streamlined `sudo daemon install -v vX.Y.Z` that stands up *all* layers in a single command is documented in `requirements/install.md`.

---

## 9. Open follow-ups (tracked in task list)

- **(active, DEFERRED 2026-06-22) Out-of-band watchdog cron cannot self-heal — needs Full Disk Access (`e2e-test-history.md` TC-05).** Confirmed live: modifying root's crontab on modern macOS requires **FDA (TCC)**; the launchd daemon and any non-Terminal automated process lack it, so their `crontab` write hangs and is killed (`crontab -: signal: killed`) — so the rail neither self-heals (F12's "mutual re-install" is false here) NOR can be scripted-restored (even the designed `RefreshWatchdog` fails). The rail is currently **DOWN** on the live machine; a clean restore needs `daemon install`/self-update from a **Terminal with FDA granted**. The in-band mesh still self-heals all *partial* attacks (TC-03/04); only the rare *total atomic teardown* recovery is affected. **Owner decision (2026-06-22): leave the watchdog for now → SUPERSEDED 2026-06-29:** after the 2026-06-29 incident (protection fully down, §6 limitation 12) the owner approved building the proper fix as **FEATURE 18** (ADR-0020): replace the cron rail with a **launchd-based out-of-band agent the daemon can manage without FDA** + a **signed offline engine backup**. ADR-0020 **reverses ADR-0016's cron choice**. Verify against `e2e-test-history.md` TC-16/TC-17/TC-18 (TC-17 supersedes the deferred TC-05); update the FEATURE 12 §4 row's honest-limit when it ships.
- ~~**#37 — activate user-mode platform mesh** so skill-protector fires on its 5-min schedule.~~ **RESOLVED by FEATURE 8** (see §6). The separate user-mode-mesh approach was superseded: the system platform now steps down to the console user at runtime (`run_as: current_user`) to fire the skill-protector job on schedule. (The live 5-min cadence is not re-verified here — daemon inspection is out of bounds — but it is resolved by design + artifacts.)
- ~~**#38 — `scripts/build-platform.sh` should `make bundle` each plugin** before building.~~ **SHIPPED (PR #44).** The build script now rebundles plugins before building the platform, preventing v0.12.0→v0.12.1-style hotfixes from stale bundles.
- **(unfiled) Remove `~/.creds/focusd_ed25519_private.pem`** and rely on the GH Actions secret only. Highest-impact security hardening still pending.
- **(unfiled) Move the source repo to a path Claude (uid 502) cannot read**. The cheapest defense against Claude-as-threat specifically.
- ~~**(unfiled) FEATURE 12 — out-of-band watchdog** is **defining** (ADR-0016 accepted)... Awaiting human gate before build.~~ **SHIPPED 2026-06-17 (PR #52).** Closes the total-atomic-teardown gap via a cron rail running a second copy of the daemon binary. Verified live (one-command install, redaction-safe status, total-teardown rebuilt in ~5s). The design questions raised here were resolved in DESIGN/build per ADR-0016.
- ~~**(unfiled) FEATURE 14 — mesh argv leak minimization** is **defining** (ADR-0018 accepted 2026-06-19)... Awaiting human gate before build.~~ **SHIPPED 2026-06-19 (PR #60).** Strips the mesh command line to role + mesh marker and makes the masked roster file the single source of truth, so a `ps` read no longer yields the three `launchctl bootout` keys (or the channel/asset/interval). **Live-verified** on a real launchd test-mesh (argv clean of all 3 labels; self-heal at parity-or-better); the prod-minimal argv, the masked-file roster read, and the plist migration matrix are **unit/integration-verified only**. **Still gated on the product owner:** a live PROD-mesh deploy + live self-update migration (not yet exercised). **Honest limit unchanged:** `argv[0]` (binary/workdir path) stays visible to root no matter what; stale background-task records iceboxed.
- ~~**(unfiled, tracked) ADR-0015 fetch-storm bug still open.** Because of it, the platform v0.15.0 binary for the F12 deploy was **placed manually** rather than fetched...~~ **RESOLVED (PR #54).** The rate-limited GitHub API call was replaced with a **direct release-download URL**, killing the fetch-storm — so deploys no longer need the manual pre-populate workaround. This (with ADR-0017 / #55, §6 limitation 10) closes the platform-fetch/self-recovery story: the out-of-band watchdog (F12) can now self-fetch the platform binary, so auto-recovery is self-sufficient. (No standalone ADR-0015 doc exists; the decision is tracked inline here.)
- **(RESOLVED, daemon-v0.5.3) Wrong download identity for the protection-engine binary across all rebuild paths** — see §6 limitation 10 + ADR-0017. Every recovery path baked a wrong/empty download identity, so auto-recovery 404'd; masked because the binary had been hand-placed. Fixed: derive the deterministic identity once, delete the knob, shipped via in-place self-update. **Verified live** (engine self-heal ~4s; full-teardown recovery ~45-60s — `e2e-verification.md` V1/V2). This closed the **same self-recovery hole** the ADR-0015 fetch follow-up was about — auto-recovery is now self-sufficient.
- **(RESOLVED, daemon-v0.5.4) Observability gap: the protection engine logged to `/dev/null`** — found during V7 white-box verification: the engine child's stdio was discarded, so engine + plugin errors/warnings were invisible (which is *why* the self-heal bug stayed hidden). Fixed: capture the engine's output to `<workdir>/platform.log` so every component is observable (the observability principle, §5). **Verified live** — `platform.log` fills with engine + all plugin job runs, 0 unexpected ERROR/WARN (`e2e-verification.md` V7).

---

## 10. Glossary

| Term | Meaning |
|---|---|
| **AMFI** | Apple Mobile File Integrity. macOS kernel subsystem that caches per-path CDHashes and rejects mismatched replacements. |
| **CDHash** | Code Directory Hash — SHA256 of a Mach-O binary's code signature directory. AMFI's cache key. |
| **Mesh** | Three coordinated launchd jobs (two workers + an ensurer/backstop) that monitor each other so killing one respawns it. *(FEATURE 10, shipped #50: each role now carries an independent random label, so the role suffix and shared prefix no longer leak the structure.)* |
| **Workdir** | The hidden, disguised directory under `/Library/Application Support/.<random>/` that holds the daemon binary, platform versions, state.db, etc. |
| **Path rotation** | Generating a new random disguised path during self-update so AMFI sees a new binary at a previously-untrusted location. |
| **Override path** | The 5-gate ceremony (GH issue + 24h wait + signed commit + BBC SHA256 + 40-word passphrase) that legitimately disables focusd. Lives in SKILL.md. |
| **Redaction rule** | The skill prose instructing Claude to refuse showing disguised tokens in its output. Behavioral, not enforced by code. |
| **DoH** | DNS-over-HTTPS. Used by network-block to resolve Steam IPs directly via Cloudflare, bypassing local DNS overrides. |
| **5-gated override** | See "Override path". |

---

## 11. How this register stays current (maintenance flow)

**The hard problem this doc solves:** decisions made in chat conversations don't end up in code by themselves. Code captures *what* but rarely *why*; commits capture per-PR rationale but not cross-cutting model. Without a register, a future Claude session (or future-Frank) has to re-derive the threat model from scratch.

**Suggested flow:**

1. **Chat → ADR.** Any time a non-trivial design decision is made in conversation (e.g. "move signing to CI", "the 5-gated override should include BBC News", "remove the user-typed-it-back exemption"), append a one-paragraph ADR to this register's §4 or §5 with a date and the one-line decision. Capture the *why* and the *alternative considered*.

2. **PR merged → status update.** After each PR merges, update the corresponding row in §4 (feature register): bump status, update honest-limitations if the PR changed the threat coverage.

3. **Release tagged → version table update.** Update §7 with the new tag.

4. **Quarterly drift check.** Run the architect agent on this doc with the prompt "compare this register against the current `git log` and the open task list; flag drift, stale claims, and undocumented features." Append findings, fix them. Catches the case where someone shipped code without updating the register.

5. **Honest-limitations review.** Before any release, ask: "did this PR weaken or strengthen any limitation already listed in §6?" If new limitations appeared, add them.

**The discipline this requires:** treat chat conversations as INPUTS, code as PRIMARY ARTIFACT, this doc as the AUDITED RECORD. The doc is not the source of truth for behavior (the code is), but it IS the source of truth for *intent + threat coverage + honest limits*. Without it, every Claude session has to reverse-engineer the model from code + git log, and Claude is too efficient at that — the model would drift toward "what the code does" instead of "what the user actually intended".

**Concrete recommendation for next session:** start each substantive Claude session by reading this register + the relevant linked design doc. End substantive sessions with a 5-minute pass: did I add a feature? Update §4. Did I weaken a limit? Update §6. Did I make a model-level decision in chat? Add an ADR paragraph.

---

*Last updated: 2026-06-29 (update/SHIP: FEATURE 17 shipped + live-verified on daemon-v0.5.6 + platform v0.16.4 — TC-14 wiped-workdir recovery (baked fallback, no permanent BLOCK, go-reviewer-confirmed) + TC-19 single live generation both PASS. §4 F17 row → shipped; §6 limitation 12 marked root causes (a)+(d) closed for steady/fresh-install; §7 version table bumped (v0.16.4 / daemon-v0.5.6). New open follow-up + e2e TC-21: post-recovery generation convergence — deleted-binary generations are invisible to install-cleanup + orphan platforms accumulate after teardown/recovery cycles (HYGIENE gap, protection stays HEALTHY, not a bypass); fix = treat deleted-binary entries as dead generations and retire them. e2e methodology lesson: gate destructive teardown steps on a positive existence check of the exact target (a split-on-space path truncation caused a TC-14 false-pass). Prior: 2026-06-29 (update/DEFINE: captured the 2026-06-29 incident — protection went fully down + did not auto-recover. New §6 limitation 12 (wiped-workdir BLOCK + Login-Item kill-switch + watchdog-needs-FDA + generation pileup + the latent-failure-from-single-vector-test lesson). Three approved-building features: FEATURE 17 daemon recovery resilience (baked fallback version + idempotent install/global singleton), FEATURE 18 resilient out-of-band watchdog (launchd rail without FDA + signed offline engine backup), FEATURE 19 deeper disguise (hide mesh tell in ps + non-clustering names + stale-record cleanup). ADR-0020 accepted — reverses ADR-0016's cron choice; ADR-0016 marked SUPERSEDED. §9 watchdog follow-up re-pointed to FEATURE 18. e2e-test-history: full live teardown matrix TC-14…TC-20, TC-17 supersedes deferred TC-05, "never claim recovery from one vector" note.) Prior: 2026-06-22 (update/DEFINE: refined F15 status behavior — `status` reflects CURRENT state only (KISS); removed the persistent "tampered → repaired Nx" 24h verdict; tamper history is log+event audit, not a status verdict; new §5 principle "Status = current state; history = log + events"; ADR-0019 refinement note; TC-07 rewritten + pending re-verify; §4 F15 row + §6 limitation 11 adjusted). Prior same-day: recorded FEATURE 15 plugin-binary integrity reconcile — §4 row [status building] + §6 limitation 11 [false-green enforcement bypass, the latent-failure class]; spec status defining→building + false-DEGRADED acceptance; ADR-0019 accepted; icebox + TC-10 follow-up for plugin config/policy integrity; e2e-test-history F15↔TC-06/07/08 mapping + ba-curator ownership invariant). Prior: 2026-06-19 (F14 PR #60). Maintainer: Frank Sun + Claude (joint).*
