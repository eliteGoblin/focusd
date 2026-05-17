# Daemon — Design (self-protection & lifecycle)

**Status: agreed, NOT built.** Layer-1 deep-dive. Hub:
[README.md](./README.md) · companion:
[self_protecting_reconcile_platform.md](./self_protecting_reconcile_platform.md).

> **Read first — framing.** focusd is **not security software**. There is
> no external attacker. The only adversary is **the user**, with root,
> impulsively trying to disable their own focus tool. The daemon
> optimizes for **friction + fast self-heal against impulse**, not
> crypto-security. "Obfuscation" here = slowing a *casual* attempt, never
> a security claim. A researched, deliberate attack always wins (honest
> ceiling, §9) — that residual is the **server's** job, later.

## 1. The one idea

We do **not** build a crash/reboot supervisor. macOS **`launchd`** is
that — Apple's init, runs everything, battle-tested. We delegate survival
to it and add only one thing: *keep the launchd registration + binary
present.*

```
launchd ─ handles: crash · kill -9 · reboot · login   (free, proven)
   ▲
   │ weak point: delete the .plist or binary → launchd
   │             has nothing left to relaunch
   │
 our ONLY added logic: 2 daemons + 1 ensurer that
 recreate each other's plists / binary
```

## 2. Bootstrap — user has one binary, runs it once

One binary, **3 modes** (argv):

```
$ focusd install            ← user runs ONCE
      │ copy self → <workdir>/bin/<ver>/
      │ write launchd plist(s) pointing at <workdir>
      │ launchctl bootstrap
      ▼
  launchd now owns it; the terminal can exit
      ├─ mode "daemon"  → the reconcile loop (launchd runs this)
      └─ mode "ensure"  → periodic recreate job (launchd StartInterval)
```

Mode picked at install by privilege:

| Installed as | launchd type | Runs | Strength |
|---|---|---|---|
| **root** | **LaunchDaemon** `/Library/LaunchDaemons` | at **boot**, as root | strong (system mode) |
| user | **LaunchAgent** `~/Library/LaunchAgents` | at **login**, as user | best-effort (user can edit own agents) |

## 3. Three launchd jobs — mutual recreation

```
        ┌──────── ensurer  (StartInterval ~60 s) ──────────┐
        │   recreates A & B plists/binary, then exits      │
        ▼                                                  ▲
   daemon A  ◄─── each recreates the other ───►  daemon B ─┘
   KeepAlive + RunAtLoad              KeepAlive + RunAtLoad
   (reconcile platform + watch partner)
```

| Job | launchd key | Role |
|---|---|---|
| A, B | `KeepAlive` | restart on crash / kill |
| A, B | `RunAtLoad` | start at **boot/login** ← this *is* reboot survival |
| ensurer | `StartInterval` ~60 s | recover when **both** A & B are gone (nothing alive to self-heal). ~1 min recovery; macOS may coalesce timing slightly |

3 plists, each rebuilt by the others → finite, no infinite regress.

## 4. What `daemon` mode does each tick

Stateless **process**; state = small files (kubelet : etcd shape).

```
every ~10s:
  v = read <workdir>/version                  # desired platform version
  bin/<v> missing?   → download + SHA-256 from GitHub (HTTPS)
  no process at bin/<v>/platform? → start it  (flock = singleton)
  partner plist/binary missing?  → recreate it
  <v> crash-looping? → write bad-<v>, fall back to <workdir>/good
```

No memory → re-derived each tick → crash-safe.

## 5. Concern → how it's addressed

| User does… | Response |
|---|---|
| Cmd-Q / crash | launchd `KeepAlive` relaunch (~≤10 s); partner also notices |
| `kill -9` | same |
| **reboot** | launchd `RunAtLoad` at boot/login — automatic |
| delete the binary | reconcile restores from partner copy / re-download |
| delete a plist / `launchctl bootout` | partner or ensurer reinstalls → launchd resumes |
| swap in a bad binary | hash ≠ GitHub's → discard + re-download genuine |
| bad version crash-loops | rollback to `good`; skip `bad-<ver>` |
| delete **all** plists+binaries & kill all within the ~1 min window | **wins — accepted ceiling (§9)** |

## 6. Workdir & obfuscation — **REQUIRED (TODO, not yet built)**

> Status: **wanted, must build.** NOT scoped out. Currently the daemon
> runs from where it was installed with a fixed default workdir and
> fixed labels `com.focusd.daemon.{a,b,ensure}`. Both items below are
> required follow-up work, tracked.

- **Self-relocation + randomized per install.** At `install`: generate
  a random, Apple-metadata-looking **workdir path** (create if absent),
  **copy the binary into it**, run from there; randomize the launchd
  **Label + plist filename** and the **binary basename**. Set once,
  persisted, never re-randomized. app_mon v0.6.1 already ships
  relocation + random Apple-looking names — **port it.**
- **No registry file.** Recognition is **structural + cryptographic**,
  not a stored list:
  - *enumerate* candidates by structure (processes/plists tied to the
    daemon's own install dir, derived live from `os.Executable()`);
  - *recognize* a genuine peer by **verifying its binary's signature
    with an embedded Ed25519 public key** (private key off-device, in
    the release pipeline). Unforgeable, tokenless, OS-portable, behind
    the OS interface (D14). Stops decoys; no greppable marker; nothing
    to corrupt or leave as garbage; idempotent.
- **Honest split:** recognition (the signature) *works* — but it does
  **not hide**. The same enumerate+verify the daemon uses, a rooted /
  AI-assisted user runs too (symmetric). Randomized names buy only
  seconds vs the *casual* attempt. Durable weight is the server, not
  secrecy. Same ceiling.
- **Self-update:** done by the twin (sibling-driven, staggered,
  last-good rollback), reusing the platform-update pattern; a daemon
  never replaces its own running binary (D15).

```
<workdir>/                  user: ~/Library/…   system: /Library/…
  version   good   bad-<v>  ← 4-file state
  bin/<good>/   bin/<v>/    ← 2 binaries only
  logs/
```

## 7. OS interface

All OS verbs (write/load plist, run-mode detect, process-presence,
relocate, download) sit behind **one interface** (mirrors the platform's
`osadapter`, §16). macOS = launchd; Linux later = systemd; Windows = SCM.
Reconcile *logic* is OS-agnostic; only verbs differ. macOS impl only now.

## 8. Single responsibility (what it does NOT do)

Daemon = keep the **correct platform version running + alive +
self/partner/launchd present.** Nothing else: no policy, no plugins, no
server, no credentials. **≤500 LoC, stdlib-only.**

## 9. Honest ceiling

Impulsive attacks (kill, rm, delete plist, reboot) → self-heal in
seconds, or relaunch at boot. A **researched, deliberate** attack
(Recovery Mode, disable launchd, scripted simultaneous wipe) → **wins.**
This is not security software; it raises the cost of the *impulse* past
the point where the urge passes. The deliberate residual is the
**off-box server's** job (detect silence / hold the commitment) —
tracked in [server_requirements.md](./server_requirements.md).

## 10. Uninstall — the commitment gate (built)

`daemon uninstall` (prod = user/system) does **not** remove anything
immediately. It is gated by a deliberate, multi-hour ritual whose only
purpose is to outlast an *impulse*: the urge to rip out one's own focus
protection is loud but short-lived; left a few hours it fades, and the
gate makes the easy path slower than the urge.

**The ritual — a 3-step ratchet:**

| Step | User must… | Then |
|---|---|---|
| 1 | transcribe shipped passage A by hand (~5–10 min) | wait **2 h** |
| 2 | (after 2 h) transcribe passage B | wait **4 h** |
| 3 | (after 4 h) transcribe passage C | **teardown proceeds** |

- 3 distinct passages are **`go:embed`-ed** in the binary; a typed
  transcription is accepted at **≥97 %** normalized similarity
  (Levenshtein — an honest typo over ~1800 chars does not nuke the
  effort) **and** only if it took **≥60 s** (instant submit ⇒ pasted).
- Progress is **never reset by being early** — running before a wait
  elapses just prints the time remaining. Waits are anchored to each
  step's own completion time.
- State is one **HMAC-SHA256-signed** file at a deterministic per-mode
  path (`SupportRoot/.com.apple.diagnostics.ug`; user vs system never
  share it). **Any** tamper — bad MAC (hand-edit), corrupt, missing, or
  a **backwards clock** — silently **resets the user to step 1**.
  Editing the file therefore only costs the user their own progress;
  cheating is self-defeating, and there is no hard-blocked state to get
  stuck in.
- `uninstall --abort` discards progress (chose *not* to quit).
- **e2e/test installs bypass the gate entirely** (the `e2e`-tag build),
  so CI teardown is deterministic and never blocks for hours.

**Honest ceiling (same as §9):** the binary is open source, so a
determined root user can read the secret/passages, `strings`+paste, or
just `launchctl bootout`+`rm` manually. The gate is **casual-grade by
design** — its durable lever is the *real-time delay*, not crypto.
Unforgeable cooldown enforcement is the off-box **server's** job (D11).
Full post-teardown cleanup (signal platform → stop plugins → remove
workdir) is tracked separately in issue #22.
