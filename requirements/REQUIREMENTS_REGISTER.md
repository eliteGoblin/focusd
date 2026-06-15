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
| **Daemon bug fixes** | Bug 1 (config staleness), Bug 2 (atomic install + rollback), Bug 3 (no auto-resolve from reconcile loop) | Foundational reliability fixes | ✅ shipped (v0.10.0 + daemon-v0.1.0) | None outstanding |
| **`daemon status` health snapshot** | Read-only health read that NEVER leaks disguised tokens — closes the "indirect question whose answer is a bypass recipe" path. `daemon status` reports only daemon-owned facts (mesh roles up / platform process / version) and **delegates** plugin/protection detail to a new `platform status`, so the daemon stays plugin-agnostic (KISS) | ✅ shipped (FEATURE 9, #45; redaction structural per ADR-0011; KISS layering per ADR-0012) | Status is a read, not a protection; per-protection recency is a last-run-status proxy (not a live re-probe) per ADR-0012; mesh/admin lines read "unknown" without sudo; age buckets coarse by design |
| **Platform singleton enforcement (daemon-held flock)** | Both mesh roles were independently starting a platform → two platforms on one healthy install (double plugin runs + DB contention; surfaced by `daemon status`). A crash-safe OS advisory lock held by the **daemon** lets exactly one daemon supervise the single platform; the loser starts nothing | ✅ shipped (#48; daemon-layer only; decision per ADR-0013) | Only macOS double-launches today (mesh is macOS-only); Windows/Linux carry the lock for future-readiness with no mesh yet to dedup |
| **Mesh label decorrelation + faster self-heal** | The 3 mesh entries shared one random base + role suffix (`.a`/`.b`/`.ensure`) — find one, grep the prefix, all three fall out; and the suffix advertised the backstop. Each role now gets an independent random label (no shared base, no role tell), the roster is persisted as a XOR-masked workdir file (reusing FEATURE 3's mask), and the heal loop tightens ~10s → ~2s so single-entry manual removal loses the race. Closes #20 | 🔧 defining (FEATURE 10; decision per ADR-0014) | Casual-grade friction only: defeats `cat`/`ls` + the `grep <prefix>` cluster-find + slow manual removal; does NOT stop reading the binary to recover the XOR key, the daemon's own un-mask path, or a scripted atomic bootout+rm of all three |

---

## 5. Cross-cutting design principles

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

---

## 7. Versioning and release cadence

| Component | Tag prefix | Current latest | Notes |
|---|---|---|---|
| Platform | `v*` (e.g. `v0.14.0`) | v0.14.0 | Triggers `platform.yml` CI |
| Daemon | `daemon-v*` (e.g. `daemon-v0.5.1`) | daemon-v0.5.1 | Triggers `daemon-release.yml`, signed in CI. Daemon status (FEATURE 9) + singleton flock (ADR-0013) shipped in the daemon-v0.5.x line |
| Legacy app_mon | `appmon-v*` | (none since refactor) | Triggers legacy `release.yml`; scoped down in PR #39 so platform tags don't trigger it |

Platform releases ship bundled plugin binaries embedded via `//go:embed` (`platform/internal/bundle/data/<plugin>/<plugin>`). Bundled binaries are force-committed past `data/.gitignore` to ensure CI builds them in. **Hygiene gap (#38):** `scripts/build-platform.sh` does not auto-rebundle plugins; bundle staleness caused v0.12.0 → v0.12.1 hotfix. Should be fixed.

---

## 8. Deploy mechanics (current state on Frank's Mac)

- Platform: deployed via daemon-managed pre-populate workaround (`<workdir>/bin/<version>/platform` + write desired version). Daemon swaps within ~10s.
- Daemon code: deployed via `sudo daemon self-update <daemon-tag>` which path-rotates to a new disguised location (FEATURE 1.5).
- Skill files: written into `~/.claude/` by the skill-protector plugin. Re-injection now runs on schedule (every ~5m) via FEATURE 8's privilege-drop — no longer a manual step (#37 resolved).

---

## 9. Open follow-ups (tracked in task list)

- ~~**#37 — activate user-mode platform mesh** so skill-protector fires on its 5-min schedule.~~ **RESOLVED by FEATURE 8** (see §6). The separate user-mode-mesh approach was superseded: the system platform now steps down to the console user at runtime (`run_as: current_user`) to fire the skill-protector job on schedule. (The live 5-min cadence is not re-verified here — daemon inspection is out of bounds — but it is resolved by design + artifacts.)
- **#38 — `scripts/build-platform.sh` should `make bundle` each plugin** before building. Prevents v0.12.0→v0.12.1-style hotfixes from stale bundles.
- **(unfiled) Remove `~/.creds/focusd_ed25519_private.pem`** and rely on the GH Actions secret only. Highest-impact security hardening still pending.
- **(unfiled) Move the source repo to a path Claude (uid 502) cannot read**. The cheapest defense against Claude-as-threat specifically.

---

## 10. Glossary

| Term | Meaning |
|---|---|
| **AMFI** | Apple Mobile File Integrity. macOS kernel subsystem that caches per-path CDHashes and rejects mismatched replacements. |
| **CDHash** | Code Directory Hash — SHA256 of a Mach-O binary's code signature directory. AMFI's cache key. |
| **Mesh** | Three coordinated launchd jobs (two workers + an ensurer/backstop) that monitor each other so killing one respawns it. *(FEATURE 10, defining: their labels are being decorrelated so the role suffix and shared prefix no longer leak the structure.)* |
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

*Last updated: 2026-06-15. Maintainer: Frank Sun + Claude (joint).*
