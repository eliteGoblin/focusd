# Solution: focusd 3-layer self-protecting platform

**Status: FOR REVIEW — not yet implemented.** Layer 2/3 (platform +
plugins) already exist (Phases 0–6). Layer 1 (daemon) and the server are
**not built**; this document is to be reviewed and agreed before any
daemon code is written.

Supersedes the earlier 2-worker+lease and disposable-worker variants and
`requirements/support_plugin_platform_refactor/enforcement_mode_server_managed.md`.

---

## 0. Glossary — the whole system in 3 lines

- **Platform** = the *brain*. The only part that talks to the server;
  owns policy; runs plugins.
- **Daemon** = the *bodyguard*. The only hard-to-kill part; keeps the
  correct version of the platform alive. No server, no policy.
- **Plugin** = a *hand*. One job the platform runs: short-lived (cron)
  or long-running (service).

## 1. Purpose & threat model

focusd is a personal commitment device: while focusing, the user adds
restrictions that must be **hard to remove later, in a weak moment**.

- Adversary = **the user themselves, with root / the app's privilege**,
  during low willpower. Not a remote attacker.
- Goal = **maximize the gap between *impulse* and *circumvention*** — the
  fast path fails in seconds and escalates; the deliberate path is slow
  enough that the urge passes.
- **Honest ceiling (accepted):** root + deliberate effort (wipe the
  install path, Recovery Mode, reinstall) always wins. Not claimed
  otherwise.
- **Accepted now:** with no server yet, if the user wipes the install
  path the app stops — *that is fine for this stage*. The off-box server
  (backup/restore + delayed-release commitment) is the real
  differentiator and **will be built** (agreed).

## 2. Core principle: no inside door handle

Two distinct bypasses:

| Attack | Defense |
|---|---|
| **Kill it** (process / autostart) | daemon pair + launchd + ensurer |
| **Ask it to stop** (config/flag/cmd) | **there is no such input** |

Locally the system is **tighten-only**: nothing local relaxes policy.
Relaxation will only ever come from the future off-box server — signed
and time-delayed (a Ulysses contract). A global "disable for upgrade"
bit is **rejected** (it *is* the inside handle, and is fragile).

## 3. Architecture: controllers all the way down

Three layers, one responsibility each, same reconcile pattern at every
layer (the proven k8s control-loop shape):

```
LAYER 1  daemon (a PAIR, mutually monitoring)         ← tiny · stable · rarely released
         single job: "the correct version of the platform is running & alive"
         kubelet/Deployment-controller for ONE pod
                │ supervises (version + liveness only)
                ▼
LAYER 2  platform  (long-running service)             ← feature-rich · released often
         talks to server · owns policy logic ·
         schedules JOB plugins · supervises SERVICE plugins
                │ supervises (schedule / health / restart)
                ▼
LAYER 3  plugins   (job = cron, or service = long-running)   kill-steam, browser-monitor
```

`kubelet → pod  ::  daemon → platform  ::  platform → plugins.`
Each layer is just *observe vs desired → act*, at a different scope.

**Why this is the long-term architecture:** separation by *rate of
change*. The thing that must never break (daemon) almost never changes;
the things that change constantly (platform, plugins) cannot brick the
survivor. Upgrade-risk is isolated in the tiny stable layer.

## 4. Layer 1 — Daemon: key features

A **pair** of identical tiny daemons. Each does **only**:

1. **Right version running** — compare the platform's running version to
   the local *signed* desired version; if wrong, perform the health-gated
   swap (§5).
2. **Platform alive** — platform heartbeat stale → restart it.
3. **Self/peer/autostart alive** — respawn the peer daemon; repair
   launchd entries (its own + peer's + platform's). A dead process can't
   fix its own autostart, so each daemon repairs **both** entries
   (monitoring both is simpler than "only the other"; self-repair is a
   harmless no-op when fine).

**The daemon must NOT contain** (all of this is Layer 2):

- ❌ policy / blocklist logic
- ❌ plugins (job or service)
- ❌ server / API communication
- ❌ reporting / telemetry
- ❌ config parsing beyond "which version"

**The one rule (KISS test):**

> If it changes when a *feature, policy, or plugin* changes, it does NOT
> belong in the daemon.

The daemon changes only when the *supervision mechanism itself* changes —
almost never. Target: a few hundred boring lines, no churn-prone deps,
stable for years. It is the thing that safely upgrades everything else,
so it must be the thing that itself almost never needs upgrading.

**Anti-kill mechanics:** 2 daemons mutually respawn each other; a 3rd
launchd job (`ensurer`, `StartInterval` ~5 min) recreates daemon entries
for the total-wipe case; daemons recreate the ensurer entry → finite
mutual recreation. launchd restarts processes (~10 s throttle floor);
the live partner ~5 s. To kill for good: delete all 3 launchd entries +
all processes within one window — survivors recreate the rest.

## 5. Layer 1's one dangerous path: health-gated version swap

The single most safety-critical code. A blind "kill, replace, pray"
turns a bad release into a crash-loop with **no enforcement = accidental
freedom** — the fail-open we must never ship.

**Strategy = Recreate (replicas=1), NOT blue-green.** One platform, one
flock → old and new cannot coexist. So:

1. **Trigger:** desired (version file) ≠ the running binary's version.
2. **Old exits** (daemon SIGTERM→SIGKILL) → kernel frees the flock.
3. **Ensure the desired binary is present & genuine:** daemon fetches
   binary + SHA-256 from **GitHub over HTTPS** (GitHub releases assumed
   legit). If a local `bin/<desired>/platform` exists but its hash ≠
   GitHub's → **discard and re-download**. (This defeats a self-dropped
   bad binary: you can't make it match GitHub's published hash, so the
   daemon replaces it with the genuine one.) Hash = corruption guard;
   on-disk-tamper-after-install defense = server-signed record (later).
4. **Start it** → it grabs the flock. Brief gap (old gone, new not up)
   is covered by durable state (hosts/DNS stay applied).
5. **Rollback:** new crash-loops (fast exits, N in a window) → write
   `bad-<ver>`, **fall back to last-known-good**, report. `good`
   advances to a version only **after** it stays up T seconds
   (promotion). Never retry a `bad-` version.

### Persistent state (daemon is a stateless *process*, state is files)

The daemon keeps **no in-memory state** (crash-safe — re-derived every
tick). The small durable facts live in files it reads each tick:

```
<dir>/version          desired version (platform writes; daemon reads)
<dir>/good             last-known-good version (rollback target)
<dir>/bad-<ver>        crash-looped versions to skip
<dir>/bin/<good>/…     2 binaries only: good + desired
<dir>/bin/<desired>/…
```

Like kubelet (stateless) over etcd (state) — here "etcd" = 4 small
files. **Fail closed, never open.**

## 6. Layer 2 — Platform: key features

The long-running service (Phases 0–6, already built):

- **Talks to the server** (sole server-facing point; server itself is
  deferred — a clean stub seam now): fetches policy + desired version,
  reports status. Writes a plain **version file in its own folder**
  (Layer 1 reads only this). *No heartbeat in v1.*
- **Owns policy logic**: blocklists, schedules, enforcement decisions.
- **Owns plugins**: schedules **job** plugins (cron) and supervises
  **service** plugins; **downloads/updates plugins itself**. The plugin
  dir is stable and lives **outside** the versioned platform-binary path,
  so a platform version-swap never disturbs plugins.
- **Singleton**: holds a crash-safe **flock** so only one platform runs.
- **No self-update, no anti-kill, no lease.** It is freely restartable;
  the daemon owns its version and liveness. This is the big
  simplification — the feature-heavy layer carries none of the
  hard-to-kill or upgrade complexity.

## 7. Layer 3 — Plugins

External signed binaries. **job** (short-lived, cron-scheduled) or
**service** (long-running, supervised). Built: `kill-steam` (process
kill), `browser-monitor` (embedded osascript, kill on blocklist). The
existing no-overlap run-lock prevents overlapping job runs.

## 8. Daemon ↔ platform contract

KISS, file-based — no IPC, no heartbeat in v1:

- Platform writes a plain **version file** in its folder.
- Daemon checks: is a platform **process present at that version**? If
  not → act (§4/§5). Process-presence only; hung-platform detection
  (heartbeat) is deferred until hangs are actually observed.

## 9. Version ownership (the single point that talks to the server)

- **Platform** is the only thing that **authenticates to / talks to the
  server**. It fetches the desired version and writes a plain
  **version file in its own folder**.
- **Daemon** never auths, never talks to the server, holds no
  credentials. It only: reads that version file → ensures that version
  of the platform binary is present (download from public GitHub by
  version if missing) → running → alive.
- **Download integrity:** daemon fetches binary + SHA-256 from GitHub
  over HTTPS and verifies the match — a **corruption guard only**
  (truncated/partial download). GitHub is assumed trustworthy (out of
  threat model); on-disk tamper *after* install is out of scope until
  the server-signed record (later).
- **Trust, staged:** no server yet ⇒ the version file is unsigned and
  the daemon trusts it (consistent with the accepted threat model: a
  root user can already edit local files). When the server lands, the
  file becomes **signed** and the daemon verifies signature + hash +
  monotonic version. Not built now, by design.
- Chicken-and-egg (broken platform can't fetch a fix): **accepted** —
  daemon keeps the last-good version running; fails **closed** until a
  fix ships.

## 10. Coordination: singleton lock, not lease

The earlier "lease between two workers" is **removed**. There is one
platform; it holds a crash-safe **flock** (kernel frees it on exit /
`kill -9` → no stale lock). Both daemons idempotently "ensure one
platform of good version"; the lock makes any duplicate self-exit. **It
is the only coordination primitive in the system.** "Exactly one
platform" is a load-bearing invariant (the 2-daemon design rests on it);
only the *mechanism* (flock) is swappable later. No lease, no
canary/safety-net, no version-aware peer logic.

## 11. Resilience: tiered, fail-closed

```
Tier 0 (always; nothing may block it): keep platform alive + self/peer/launchd
Tier 1 (best effort): platform pulls policy/version from server
Tier 2 (best effort): report / telemetry
```

A Tier-1/2 failure is logged + reported, **never** stops Tier 0. Server
unreachable ⇒ keep enforcing cached signed policy (fail closed).
Idempotent + atomic writes ⇒ a crash mid-action cannot corrupt; the next
tick repairs it.

## 12. What is built vs deferred

**Built (Layer 2/3, tested, on `feat/platform-refactor`):**
config, plugin discovery/validation, runner, scheduler, SQLite state;
plugins kill-steam + browser-monitor; the pure `reconcile.Decide`/`Engine`
spine + CI. All packages ≥80%, race-clean.

**Reframing:** the reconcile spine becomes the **daemon's** loop, with a
*smaller* action set (version + liveness only — no policy-enforce
actions, no lease). `DBLease` is dropped for platform coordination
(singleton lock replaces it); the no-overlap run-lock stays only for
plugin jobs in the platform.

**Deferred / NOT built (this review gates them):**
the daemon itself (pair + ensurer + health-gated swap), launchd plist
triad, GitHub binary download+verify, the off-box server.

## 13. Roadmap & sequencing (priority call)

1. ✅ Layer 2/3 (platform + plugins) + reconcile spine.
2. **Server first** — it is the actual differentiator (vs Freedom). A
   perfect 3-layer system with no commitment server is still Freedom-tier
   in practice. Do not over-invest in daemon topology before this.
3. Minimal daemon (pair + ensurer + health-gated swap), kept ruthlessly
   small per §4.
4. launchd triad + versioned binary layout + plain version file.

## 14. Decisions (resolved)

1. **Server:** off-scope now; later, off-box (Cloud Run / serverless —
   location TBD). Required of us now: keep server-talk a clean stub seam.
2. **Daemon↔server auth:** daemon never talks to the server; platform is
   sole server-facing point. Version file unsigned now → signed when the
   server lands (only later-open item: signing-key management then).
3. **Liveness:** v1 = **process-presence only**, no heartbeat.
4. **Singleton:** platform-only; crash-safe **flock**. Invariant fixed,
   mechanism swappable.
5. **Daemon budget:** Go, **stdlib only, no external deps, ≤ ~500 LoC**
   (makes §4's discipline enforceable in review).
6. **Daemon downloads:** platform binary **only**; plugins versioned and
   downloaded independently **by the platform**, into a stable plugin
   dir outside the versioned binary path.
7. **Cold start:** §9 — resolve GitHub latest once → pin; true cold
   start only (no file AND no platform); else treat missing file as
   tamper.
8. **OS portability:** daemon OS-specifics behind an interface (§16).

## 15. Honest ceiling (restated)

Root + deliberate effort defeats this. The design does not claim
otherwise. It makes the impulsive path fail fast and escalate, and the
deliberate path slow — which, for a self-discipline tool, is the point.
The commitment strength comes from the **server**, not the topology.

## 16. OS abstraction (daemon)

Every OS-specific concern the daemon touches goes behind **one small
interface** (mirrors the platform's existing `osadapter` — same pattern,
not a new idea). Core daemon logic is OS-agnostic; per-OS code is
isolated build-tagged implementations.

Behind the interface:

| Concern | macOS (built first) | Linux / Windows (later) |
|---|---|---|
| autostart | launchd plists | systemd / SCM |
| singleton | flock | flock / named mutex |
| process-presence | `pgrep`/`ps` | `/proc` / Toolhelp |
| install/exec/download | exec + HTTP GET | same |

KISS: the interface is tiny and **only the macOS impl exists now**.
Adding an OS = one new impl behind the interface, zero core changes.
This keeps the ≤500-LoC daemon portable without speculative code.
