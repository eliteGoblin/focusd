# focusd — Design (single source of truth)

This is the **hub**. It tracks status, **all major decisions**, and links
to deep-dive docs. When a section grows too big, it gets its own markdown
and is linked from here — keep this page scannable.

_Last updated: 2026-05-17 · branch: `feat/platform-refactor`_

---

## 1. What focusd is

A personal commitment device: while focusing you add restrictions that
must be **hard to remove later, in a weak moment**. Adversary = you, with
root, low willpower. Goal = widen the gap between *impulse* and
*circumvention*; not impossibility (honest ceiling accepted).

**Design philosophy.** Success = the user actually stays focused —
measured by behaviour, not technical purity. A root user can always win
*technically* (honest ceiling). Therefore **psychological and social
friction are first-class design levers, equal to technical ones**:
anything that makes the bypass not worth the effort, or visible to
someone the user doesn't want to disappoint, is a valid part of the
solution. Build for "it actually helps the user", not "it's technically
unbreakable" — the latter is impossible and is not the goal. Concrete
examples already in the design: per-install randomized names (no
copy-pasteable forum bypass), auto re-arm from the server, accountability
notification.

**Defense in depth, friction not perfection.** The primary threat is an
*addicted, desperate mind* — creative, possibly AI-assisted, with public
source. No single layer is perfect; the design stacks independent layers
(launchd resurrection · twin+ensurer · camouflage · crypto recognition ·
off-box server re-arm · accountability) so breaking it becomes
**impractical enough that the person doesn't even try**. "Good = works
in practice", not "perfect in theory". Two layers do different jobs and
must not be confused: **recognition** (crypto: unforgeable "this is
genuine appmon") *works*; **hiding** (camouflage) is only casual-grade —
an AI + public source defeats it, so the durable weight sits on the
**server**, not on secrecy.

3 layers, "controllers all the way down" (k8s shape):

- **Platform** = the *brain*. Talks to the server; owns policy; runs plugins.
- **Daemon** = the *bodyguard*. Hard-to-kill; keeps the correct platform
  version alive. No server, no policy.
- **Plugin** = a *hand*. One job: cron (short) or service (long).

## 2. Status — what's built vs not

| Area | State |
|---|---|
| Platform Phases 0–6 (config, plugin discovery/validation, runner, scheduler, SQLite state) | ✅ built, tested, on branch |
| Plugins: `kill-steam`, `browser-monitor` | ✅ built, tested |
| Reconcile spine (`platform/internal/core/reconcile`, pure Decide + Engine) | ✅ built, 91%, race-clean |
| Platform CI (`.github/workflows/platform.yml`) | ✅ added |
| **Daemon (Layer 1)** | ❌ not built — design agreed, see deep-dive |
| **Server** | ❌ not built — requirements tracked |

Code overview: [`platform/README.md`](../../../platform/README.md).

## 3. Major decisions log (the tracker)

| # | Decision | Status |
|---|---|---|
| D1 | 3-layer "controllers all the way down" (daemon → platform → plugins) | accepted |
| D2 | Daemon = stateless *process* over small persistent files; only job = correct platform version running + alive + self/peer/launchd; **≤500 LoC, stdlib-only** | accepted |
| D3 | **No inside door handle**: local is tighten-only; relax only via future off-box server | accepted |
| D4 | Reconcile is the model; pure `Decide` + `Engine` | ✅ built |
| D5 | Upgrade = **Recreate (replicas=1), no blue-green**, no-overlap; keep 2 binaries; rollback to last-known-good; `bad-<ver>` marker | accepted |
| D6 | Singleton = **platform-only**, crash-safe **flock** (invariant fixed, mechanism swappable) | accepted |
| D7 | Liveness v1 = **process-presence by binary path**; no heartbeat; **no daemon↔platform port/API** | accepted |
| D8 | Version of record = persistent **file** the daemon reads directly; never queried from the platform | accepted |
| D9 | Daemon downloads **platform binary only**; plugins independent, platform-owned, in a stable dir outside the versioned path | accepted |
| D10 | Download integrity = **SHA-256 from GitHub/HTTPS** (corruption guard); **GitHub releases assumed legit**; hash-mismatch ⇒ re-download (defeats self-dropped bad binary); real on-disk-tamper defense = server-signed record (later) | accepted |
| D11 | Server: **off-box mandatory**, deferred; platform is sole server client; daemon never talks to server; enforcement **offline-complete / fail-closed**; v1 server scope = policy + version + signed records | accepted |
| D12 | OS specifics behind **one interface** (mirrors `osadapter`); macOS first | accepted |
| D13 | Plugins: job/service model; `kill-steam` + `browser-monitor` | ✅ built |
| D14 | Peer recognition = **own embedded Ed25519 public key** (verify release signature), OS-portable, behind the OS interface. **Not RSA** (bigger, no benefit). macOS notarization / Team ID kept **separately** for Gatekeeper/install trust, *not* the recognition mechanism. Recognition works; it does not hide (symmetric — adversary runs the same check) | accepted |
| D15 | Daemon **self-updates via the twin** (sibling-driven, staggered, last-good rollback), reusing the platform-update pattern; daemon never replaces its own running binary | accepted |

## 4. Document index

| Doc | Scope | Status |
|---|---|---|
| [daemon_design.md](./daemon_design.md) | **Daemon** self-protection & lifecycle (launchd, bootstrap, pair+ensurer, concern→response) — deep-dive | agreed, not built |
| [self_protecting_reconcile_platform.md](./self_protecting_reconcile_platform.md) | Reconcile / upgrade / 3-layer architecture & threat model — deep-dive | agreed, not built |
| [server_requirements.md](./server_requirements.md) | Server requirements & TODO tracker | not started |
| [release_and_shipment.md](./release_and_shipment.md) | Signing / release / shipment (own Ed25519 now; Apple notarization far-future, additive) | not started |
| [platform/README.md](../../../platform/README.md) | Built platform + plugins (code-level) | current |
| [platform_refactor_plugin.md](../../requirements/support_plugin_platform_refactor/platform_refactor_plugin.md) | Original refactor spec (acceptance criteria) | satisfied (Phases 0–6) |

> Rule: a topic that outgrows its section → new markdown + link added to
> the table above. This page stays the index + decision log.

## 5. What's next

1. **Design: closed.** Only the daemon remains to build (server deferred).
2. Build the **minimal daemon** (Layer 1) per
   [daemon_design.md](./daemon_design.md): stateless reconciler — read
   `version` file → `pgrep bin/<v>/platform` → download+SHA-256 if
   missing → start; flock singleton; launchd pair+ensurer; OS-interface;
   rollback via `good`/`bad-` files; ≤500 LoC stdlib-only.
3. Server later — see `server_requirements.md` (SR-F-1..4 first).

Honest ceiling: root + deliberate effort defeats this. Commitment
strength comes from the **server**, not the topology.
