# Focusd — Icebox (speculative ideas, not committed)

> Forward-looking ideas that are **not** on the near-term plan. They may never
> ship. This is distinct from the committed backlog in the
> [Requirements Register](./REQUIREMENTS_REGISTER.md). Each entry states the
> durable idea, why it might matter, its tension with current philosophy, and
> the open question to resolve before promoting it to a feature spec + ADR.
>
> **Provenance:** the two server-managed entries below were migrated from legacy
> `app_mon/requirements/` design notes on 2026-06-15 (the legacy `app_mon`
> module is being removed in PR #49). Implementation-level detail in the
> originals (Go interfaces, SQLite schemas, AppleScript snippets, folder paths)
> was intentionally dropped — that belongs in code, not in product docs.

---

## Server-managed enforcement mode (server owns the commitment)

**Maturity:** [exploring] · **DEFERRED (2026-07-14, Frank — ADR-0022): not on the
plan now.** Standing decision: focusd stays **local-first** (resilience + friction);
**no off-device server for now.** Rationale (settled, do not re-pitch as an open
question): a server would only be a config/lock **manager** — enforcement is
**inherently local**, so it does not make the local agent run more resiliently or
un-stoppable. Kept here only as a **possible later revisit**; do not promote without
Frank explicitly re-raising it.

**The idea (distilled).** Today the strongest commitment lever lives on the same
machine the weak-moment user controls — so a root user can eventually relax it.
Enforcement mode **removes the disable authority from the machine entirely**:

- The **server (off-box, not this Mac) owns the policy.** The client does not
  trust local config; it enforces a locally-cached, server-signed policy.
- **Asymmetry is the whole value: tightening is instant + local; loosening is
  remote + delayed.** The client has *no* local disable path — no flag, command,
  or config edit can loosen policy. It can only enforce or wait.
- **Loosening requires a server-signed, time-gated release token** (the client
  embeds only the server's public key, so it can verify but never forge). The
  server issues that token only after a user-set cool-down — a genuine
  emergency is possible but an impulse is not.
- **Fail closed, never open.** If the server is unreachable, the client keeps
  enforcing the last known signed policy. "Can't reach the server" must mean
  *restriction persists*, never *unmanaged = free* (an adversary will simply
  firewall the server). A long no-contact dead-man window (≈14 days) is the only
  auto-expiry.
- **Killing the daemon should be pointless, not impossible.** Blocks are durable
  state, re-asserted continuously; tamper without a valid token is detected,
  logged, notified, and **extends the lock** — fighting it makes it worse. This
  is the lever local-only tools (e.g. Freedom) lack.
- **Server-side liveness:** the client heartbeats; missing heartbeats are
  themselves an alert. Killing the client becomes visible to your future self
  and grants no freedom, because the policy you'd need to relax is server-held.
  → This **detection/alerting half** is now captured on its own as
  [FEATURE 13 — Heartbeat + accountability-partner alerting](./features/13-heartbeat-accountability-alerting.md),
  which the product owner flagged as "very important for addicted people." It can
  potentially ship as a lighter detection-only slice *before* the full
  policy-loosening half below; decide when promoting.

**Why it might matter.** It's the first design that delivers a commitment a
root-equipped weak-moment self genuinely cannot shortcut quickly — strictly
stronger than any purely local armor, and stronger than Freedom.

**Honest reality check (keep this).** A determined user with physical + root
access still wins eventually (recovery mode, OS reinstall, pulling the network).
This does not claim cryptographic perfection. It maximizes the gap between
*impulse* and *circumvention* — which, for a self-discipline tool, is the point.
Be honest about privilege tiers: with root, durable network/host blocks survive
daemon death; in pure user mode there is no durable block, so the commitment
must come from no-self-disable + cooldown-gated remote token + the existing
off-device NextDNS block (already the strongest anchor we have; not counted as a
focusd enforced layer — see the "NextDNS as a platform-managed plugin" idea below).

**Tension with current philosophy.**
- Introduces a **server** and a **network dependency** — a new product shape and
  a new persona (the off-box "future-you" service). The register's threat model
  and KISS isolation rule are currently single-machine; this materially expands
  scope. **Needs human sign-off before promotion.**
- The server **must be off-box** (a VPS or a service the user can't trivially
  nuke). An on-box server = no commitment. This is a hard prerequisite, not a
  detail.

**Dependencies.** Builds on the existing self-protection mesh (must land first).
Partially overlaps the Phase-2 "server sync + 24h cooling-off" sketch in
`app_mon/4_encrypted_registry_server_sync.md` and the cool-down idea in
`app_mon/further_questions_ideas.md` — this entry is the sharper, more current
framing (signed release tokens + fail-closed + dead-man valve + off-box-server),
so promote from here rather than re-deriving from those older notes.

**Open questions to resolve before promoting.**
1. Where does the server run, who owns it, and how is it kept un-nukeable by
   the weak-moment self?
2. Client↔server authentication and device-binding.
3. What exactly does the server own — blocklists/schedules only, or plugin
   enable/disable, or plugin distribution itself?
4. The legitimate-disable escape hatch: cool-down duration, and whether a
   second factor (accountability partner / hardware key) gates it.
5. Offline grace policy: keep-last vs escalate-to-strictest, and the exact
   dead-man expiry.

---

## Protection-coverage dashboard (server-collected metrics + honest status)

**Maturity:** [raw] — owner explicitly wants this as a **refinement-needed
backlog item**, not a spec yet.

**The idea (distilled).** After it authenticates, the protection engine reports
its status/metrics to a server, and the **user + an accountability partner get a
dashboard** showing whether protection is actually on — overall and **per
component** (the enforcement engine plus each plugin: kill-steam, dns-block,
etc.): "is there an issue or not." The headline metric is a
**coverage-of-online-time** number: *of the time the device was connected to the internet, how much
was it actually protected?* ("Once connected to the internet, protection should
be on.")

**Honest status, not "always green."** A device can be **offline / disconnected**
— that is a *distinct* state, not "unprotected" and not "healthy." The dashboard
must cleanly separate **offline/unknown** from **online-and-protected** from
**online-and-failing**, both overall and per component.

**Why it might matter.** The addicted user and their accountability partner get
**real visibility** instead of a fake-green light. An honest coverage percentage
("you were protected 96% of your online time") is more motivating and more
trustworthy than a binary indicator — and a partner who can see a per-plugin
failure or a coverage dip can actually act on it.

**Tension with current philosophy.**
- **Privacy / new data collection.** A server collecting client telemetry is
  **new data collection and a scope expansion** — the register's threat model and
  personas are single-machine today. What gets transmitted (status-only vs. richer
  metrics) is an open product question. **Needs human sign-off before promotion.**
- **Honest-status / observability principle (register §5 "Observability is
  non-negotiable").** "Always green" is exactly the *latent-failure* anti-pattern
  this idea is reacting against — so the dashboard must surface real
  offline/failing states, never paper over them. This idea is aligned with §5 *if*
  it stays honest; it betrays §5 the moment it shows green for a device it simply
  hasn't heard from.
- **The device is also the adversary.** The weak-moment self controls the
  reporting device, so a status surface could be **gamed** (suppressed or
  spoofed). Open question whether this is purely *informational* (and leans on
  F13's authenticated-heartbeat dead-man semantics so silence ≠ healthy) or
  something stronger.

**Dependencies.** Builds on / overlaps
[FEATURE 13 — heartbeat + accountability-partner alerting](./features/13-heartbeat-accountability-alerting.md):
**F13 is the transport** (authenticated heartbeat + partner notification); this
idea is the **metrics + dashboard + coverage view layered on top**. Also shares
F13's server / device-auth / off-box-server prerequisites — don't re-derive them.

**Open questions to resolve before promoting.**
- **Where does the coverage metric live?** Does the **device itself** track its
  own protected-vs-online time and report the computed coverage, or is coverage
  **derived server-side** from heartbeats? (Device-side is richer but more
  game-able; server-derived is simpler but coarser.) Resolve before spec.
- Plus F13's shared opens: what's transmitted (privacy), device enrollment/auth,
  and where the server lives + how it's kept un-nukeable.

---

## Secure self-update (blue-green, signed, auto-rollback)

**Maturity:** [raw]

**The idea (distilled).** A way to roll out a new daemon/agent version that the
weak-moment user cannot exploit and that can't brick protection:

- Download a **signed** new binary; verify signature **and version
  monotonicity** (block downgrades — a downgrade is a route back to a
  killable/permissive build).
- Bring the new instance up *first*, require it to pass a self-test and post a
  healthy heartbeat, hand off the guardian role atomically, **then** retire the
  old one — never a zero-guardian window.
- Keep the previous known-good binary; if the new one fails health within a
  bound, **auto-rollback** and report the failed rollout.
- Crashloop protection (backoff + rollback); the server tracks per-client
  rollout success and can **halt a bad version** (canary-self-first staged
  rollout).

**Why it might matter.** Self-protecting agents are useless if an update can
either be exploited to install a weakened build or can crashloop protection into
the ground. This makes updates safe in both directions.

**Tension with current philosophy.** The server-orchestrated parts (rollout
halt, per-client tracking) presuppose the server-managed enforcement mode above.
The blue-green + signed + monotonic + auto-rollback *local* mechanics, however,
could stand alone and strengthen today's path-rotating self-update without a
server.

**Open question to resolve before promoting.** Can the local blue-green +
auto-rollback half be delivered independently of the server (yes, likely worth
a standalone feature spec), leaving only the rollout-orchestration half coupled
to enforcement mode?

---

## Service plugins (long-running, health-checked) + privilege-tiered run modes

**Maturity:** [raw]

**The idea (distilled).** The platform/plugin refactor already shipped
job-plugins (short-lived, scheduled — e.g. kill-steam, dns-block). Two
forward-looking pieces from the original refactor note never shipped and are
worth keeping:

- **Service plugins:** long-running plugin processes the platform starts,
  health-checks, and restarts per policy (vs. fire-and-exit job plugins). The
  motivating example is a continuously-running browser/tab monitor managed as a
  service rather than re-spawned every tick.
- **Privilege-tiered run modes as a first-class concept:** a plugin declares the
  privilege it needs (user vs system) and the identity it runs as; the platform
  refuses to silently run a user plugin as root, and a user-mode platform
  cleanly skips system plugins. (Runtime step-down to the console user already
  shipped via the privilege-drop work; the broader manifest-declared tiering is
  the part still open.)

**Why it might matter.** A health-supervised service model is the natural home
for always-on monitors (browser tabs, a future filter proxy) and is a
prerequisite for the enforcement-mode heartbeat/liveness story.

**Tension with current philosophy.** Adds lifecycle complexity (start/stop,
health, restart policy) to a platform whose KISS win so far is that everything
is a stateless scheduled job. Only justify it when a real always-on plugin needs
it.

**Open question to resolve before promoting.** Is there a concrete always-on
plugin that genuinely can't be modeled as a frequent job? (If the shipped
bash browser-guard's 10s poll is good enough, this may stay iceboxed.)

---

## Self-update should clean up stale background-task records (Login Items cruft)

**Maturity:** [raw]

**The idea (distilled).** Each self-update rotates the binary path (the anti-AMFI
defense), which mints a **fresh set of launchd labels** (one per role). macOS's background-task store
keeps the **old** records around as inert cruft — surfacing as **stale duplicate
entries** in System Settings → Login Items → "Allow in the Background" (the owner
saw triple duplicates). The idea: on self-update, **deregister / boot out the
old-generation labels and remove their orphaned plists** so the background-task
store does not accumulate stale records — **only if macOS offers a clean,
supported deregistration path**; otherwise document it as an accepted limitation.

**Why it might matter.** The stale entries are both a **visual tell** (they hint
at a rotating, self-protecting agent and multiply its footprint) and **clutter**.
Removing them tightens the disguise and keeps the Login-Items surface honest.

**Honest reality check (keep this).** **Live protection does not depend on the
stale records** — their binaries are gone, so they are inert. This is hygiene and
disguise, not a protection gap. It surfaced alongside FEATURE 14 (the `ps`
argv-leak fix) and is explicitly **out of F14's committed scope** — F14 records it
as a known limitation rather than solving it.

**Tension with current philosophy.**
- **KISS / friction-not-cryptography.** Worth doing only if there is a *clean*
  supported deregistration path. Fighting an undocumented background-task store
  to scrub records risks fragile, OS-version-specific code for a cosmetic/disguise
  gain — weigh against the self-protection value before promoting.
- **Self-update is delicate.** Touching the two-copy / path-rotating update flow
  to also deregister old generations adds steps to a path that must never drop
  protection mid-change. Any cleanup must be best-effort and never block the
  update.

**Dependencies.** Sits on the path-rotating self-update (FEATURE 1.5) and the mesh
labels (FEATURE 10). Naturally pairs with FEATURE 14's disguise work but is
independent of it.

**Open question to resolve before promoting.** Does macOS expose a **clean,
supported way** to deregister an old-generation label / remove its orphaned plist
from the background-task store on self-update? If yes → a small standalone
feature. If no → keep it documented as an accepted limitation (FEATURE 14 already
notes it) and do **not** hand-hack the store.

---

## Plugin config / policy integrity (the other half of the trust story)

**Maturity:** [exploring]

**The idea.** FEATURE 15 / ADR-0019 makes the plugin **programs** continuously
authentic — verified every reconcile tick against the genuine copy carried inside
the signed platform binary, restored on tamper, recorded as a security event. But
a genuine plugin program is only as strong as **what it is told to do**. The
weak-moment owner can leave the program untouched and instead gut its *inputs* —
empty out a blocklist, drop a target app from the kill list, stretch the job
schedule so it effectively never runs. The program passes its integrity check and
runs faithfully — over a neutered policy. This follow-up extends the same
signed-desired-state + tighten-only discipline from plugin **binaries** to plugin
**config/policy**: blocklists, target-app lists, job schedules, and a "no inside
door handle" guarantee that local config can only *tighten*, never loosen.

**Why it might matter.** It closes the obvious next move after F15 lands: if you
can't swap the program, swap its orders. Without this, "plugin integrity" is only
half true, and the same false-green class (status reads ok over a plugin pointed
at an empty blocklist) can reopen at the config layer. Already tracked as the
standing regression **TC-10** in `e2e-test-history.md`.

**Tension with current philosophy.**
- **Friction, not a seal.** Same honest ceiling as F15 — root can re-tamper; this
  is fast self-heal + detection, not an unbreakable barrier. The durable weight
  stays the server-side override gate.
- **Overlaps the server-managed-enforcement entry above.** The cleanest "config
  can only tighten" story is a *server-signed* policy the client can verify but
  never forge (that entry). A local-only tighten-only check is a weaker,
  KISS-er interim. Decide which layer owns policy authenticity before promoting,
  so the two ideas don't grow conflicting trust roots.

**Dependencies.** Sits on FEATURE 15 (the binary-integrity reconcile + tamper-
event plumbing) and the platform's signed-desired-state spine.

**Open question to resolve before promoting.** Where does the authentic plugin
**policy** come from — the local signed platform bundle (like the binaries, KISS,
but only as fresh as the last release) or an off-box server-signed policy (the
server-managed-enforcement entry, stronger but heavier)? Resolve that before
turning TC-10 into a feature spec + ADR.

**Live observation (2026-07-15, v0.18.0) + the config→server direction.** On the
v0.18.0 live build, **net-block was disabled by baked default with no override
present**, and **config/policy integrity was ABSENT** — nothing protects the config
itself, so this half of the trust story is still fully open on the live machine.
Frank's stated direction: the **current local baked/override config is the KISS
interim**; **later, policy authority moves to a server** so the config becomes **hard
to fake locally** (config→server). That is the *stronger* of the two options in the
open question above (the server-signed-policy path), and it **rhymes with the
"Server-managed enforcement mode" entry**. It is a **later revisit, not now**: per
**ADR-0022** (local-first, no off-device server for now — SETTLED), moving config
authority to a server needs **Frank to explicitly re-raise it** — do not promote as an
open question until then.

---

## Install location beyond `/Library/Application Support/` — harder-to-guess / spread across less-predictable dirs

**Maturity:** [raw] — **think, not now.**

**The idea.** Today the install lives under `/Library/Application Support/` — a
convincing "I'm a legitimate app" disguise home. But it is also the **first place** a
technical owner looks. Explore a **harder-to-guess** location, or **spreading the
install across less-predictable directories**, so that even once the process-table
invisibility bar holds (FEATURE 24 / TC-30), the fallback "just go look in Application
Support" search doesn't hand over the folder.

**Why it might matter.** Reinforces the VITAL grep→workdir→`rm` defense at the on-disk
layer: process-table invisibility is worth less if the folder still sits in the one
obvious place a determined owner will open next.

**Pro / con (one line).** Application Support is a **convincing disguise home** (blends
in with real apps) but **predictable**; a less-guessable spread is harder to find but
risks looking *more* anomalous (an odd hidden dir can itself be a tell) and adds
install/recovery complexity.

**Tension with current philosophy.** Friction, not a seal (register §5) — a determined
root user who reads the source finds any location; this only raises casual-search cost.
Must not undercut self-recovery (the recoverer still has to find/rebuild the folder) or
the disguise's "looks legitimate" goal.

**Dependencies.** Sits on FEATURE 24 (process-table invisibility) + FEATURE 21 (storage
separation / recovery); interacts with the disguise + path-rotating self-update paths.

**Open question to resolve before promoting.** What location strategy stays both **hard
to guess** AND **plausible** (not itself an anomaly a scan flags), without breaking
self-recovery's ability to re-establish the folder? Resolve before turning this into a
feature spec + ADR.

---

## NextDNS as a platform-managed plugin (network-level DNS under supervision)

**Maturity:** [raw] — **consider in future, not committed.**

**The idea.** Today network-level DNS blocking (NextDNS, configured at the router
or device) is a **separate, optional, manual anchor** that lives *off* the focusd
machine — nothing in the daemon/platform keeps it live or notices if it's changed.
Bring it **under platform management as a plugin**, so the platform **ensures it
stays live** — supervised and anti-tampered the same way the other plugins are
(dns-block, kill-steam, network-block, skill-protector). If the weak-moment self
switched the profile off or repointed DNS, the platform would detect the drift and
re-assert the intended NextDNS state on its reconcile loop.

**Distinction to keep crisp (local hosts vs network DNS).**
- **dns-block plugin** already blocks at the **machine-local hosts file** — a
  platform-managed, enforced-tier plugin today.
- **NextDNS** blocks at the **network / DNS-resolver level** (every device behind
  the router), which the machine-local hosts file cannot reach. This idea is about
  the *network* layer, not the local one — the two are complementary, not duplicates.

**Why it might matter.** NextDNS is the broadest-reach anchor (covers every device
on the network, not just this Mac), yet it's the *least* self-protecting piece
because it's manual and off-box. Making it a supervised plugin closes the "user
quietly turns the DNS profile off" gap and folds it into the same live-and-genuine
guarantee the register's layered-supervision principle already demands of every
other layer.

**Tension with current philosophy.**
- NextDNS control lives **off the focusd machine** (a cloud profile / router
  setting), so a plugin would need to reach an external account/API — a **new
  network + external-account dependency** the enforced tier does not have today.
  That is a scope expansion; **needs human sign-off before promotion.**
- Anti-tamper only bites where focusd has authority. If the profile is changed from
  the NextDNS account (not the Mac), a machine-local plugin can **detect** but not
  necessarily **prevent** it — be honest about that ceiling before promising
  "supervised."

**Dependencies.** Sits on the platform's plugin-supervision + continuous-authenticity
machinery (FEATURE 23) and the reconcile loop. Independent of the deferred
server-managed-enforcement idea above, though they rhyme (both move authority off-box).

**Open question to resolve before promoting.** How does the plugin authenticate to
NextDNS and re-assert state without embedding a long-lived secret on the very machine
the weak-moment self controls — and what part of the profile can it actually enforce
vs merely detect? Resolve before turning this into a feature spec + ADR.

---

## Related ideas already captured elsewhere (do not duplicate here)

These live in their own (untracked) `app_mon/` notes and should be consolidated
separately if/when that folder is cleaned up — they are **not** re-captured in
this icebox:

- **Web filter / CONNECT proxy** (domain+subdomain blocking without MITM,
  corp-proxy chaining, PAC file) — `app_mon/phase2_web_filter_proxy.md`.
- **Encrypted local registry + Phase-2 server sync + 24h cooling-off** —
  `app_mon/4_encrypted_registry_server_sync.md` (the server-sync half overlaps
  the enforcement-mode entry above, which supersedes its framing).
- **Per-URL/path blocking, LLM content analysis, checksum-verified downloads** —
  `app_mon/phase2_future_enhancements.md` / `app_mon/future_enhancements.md`.
- **MDM-vs-self-block decision questionnaire, Yubikey/Cloud-Key friction,
  cloud-stored release pipeline** — `app_mon/further_questions_ideas.md`.
</content>
</invoke>
