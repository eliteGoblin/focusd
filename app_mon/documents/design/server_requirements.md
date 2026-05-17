# Server — Requirements & TODO (focusd)

**Status: NOT STARTED. Tracking only.** KISS — this lists *what the
server must do*, not how. Companion to
`self_protecting_reconcile_platform.md`. The server is the actual
commitment differentiator (vs Freedom); the 3-layer client is just the
robust skeleton.

## Locked assumptions (don't re-debate)

- Server is **off-box, mandatory** (on-box = no commitment).
- **Platform is the only server client.** Daemon never contacts the
  server, holds no credentials.
- Enforcement works **fully offline / fail-closed**. Server is only for
  *relax + config + report* — never on the enforcement hot path.
- GitHub releases assumed legit; the **server** (signed records) is the
  trust anchor for local-tamper, not checksums.

## Functional (SR-F)

| ID | Requirement | Phase |
|----|-------------|-------|
| SR-F-1 | Server owns + serves **desired policy** and **desired version**, signed. | v1 |
| SR-F-2 | Platform **authenticates** to the server; fetches signed payloads. | v1 |
| SR-F-3 | Payloads **signed**; platform verifies with an embedded public key (private key off-box). | v1 |
| SR-F-4 | Version record carries the **expected SHA-256** of the platform binary (local-tamper defense). | v1 |
| SR-F-5 | **Commitment:** relaxation only via a server-issued, signed, **time-delayed release token**; cooldown not client-shortcutable; tightening always immediate. | later |
| SR-F-6 | **Slow-unlock / emergency** path: deliberate, delayed, auditable. | later |
| SR-F-7 | **Client status:** platform reports last-seen / version / applied-policy hash; server records; silence is visible. | later |
| SR-F-8 | **Backup/restore:** server stores policy/state; detects local wipe; platform can restore. | later |
| SR-F-9 | **Audit log:** every policy change + token issuance (future-you auditing past-you). | later |

## Non-functional (SR-N)

| ID | Requirement |
|----|-------------|
| SR-N-1 | Off-box; serverless OK (e.g. Cloud Run); cheap; user-controlled but not trivially nukeable by present-you. |
| SR-N-2 | Server downtime must **not** weaken protection (enforcement is offline-complete). |
| SR-N-3 | **Fail-closed**: unreachable ⇒ platform keeps enforcing cached signed policy. Long dead-man window (≥ ~14 days) before any auto-expiry. |
| SR-N-4 | TLS transport; signed payloads; minimal endpoints; no secrets in the daemon; least privilege. |
| SR-N-5 | KISS / serverless-friendly: stateless request/response; platform **polls** (minute granularity fine); no long-lived connections. |
| SR-N-6 | Cheap (free/low tier) and auditable. |

## Optional / later (SR-O)

- SR-O-1 Web dashboard: set policy + view audit.
- SR-O-2 Multi-device.
- SR-O-3 **Accountability-partner notification on suspected tamper.**
  Social deterrent (not a technical control) — value = a human you don't
  want to disappoint gets told. MUST solve the noise problem or it's
  useless (alert fatigue): (a) platform sends a *clean-shutdown* check-in
  on graceful stop/sleep → silence-without-it = suspicious,
  silence-with-it = benign; (b) long threshold (~24–48 h, not minutes);
  (c) optional user-declared expected-online window; (d) two-stage —
  notify the user first, escalate to partner only if still silent.
  Honest limit: a faked checker can keep heartbeats green while focusd
  is gone → only meaningful once heartbeats carry signed proof of the
  applied policy (depends on SR-F-3 signing). Phase: later.
- SR-O-4 Stronger emergency unlock (2-person / hardware key).

## Open questions (SR-Q)

- SR-Q-1 Hosting specifics (Cloud Run? region? cost ceiling).
- SR-Q-2 Platform↔server auth mechanism (mTLS / device-bound key / token).
- SR-Q-3 Signing-key management + rotation; how the platform first gets
  the public key (ship in binary?).
- SR-Q-4 Release-token cooldown policy (duration, slow-unlock rules).
- SR-Q-5 Backup payload format + restore trigger.
- SR-Q-6 Server API / policy-schema versioning.
- SR-Q-7 Tamper-vs-offline discrimination policy for SR-O-3 (threshold,
  clean-shutdown signal, expected-online window, two-stage escalation).
- SR-Q-8 Heartbeat integrity: signed proof-of-applied-policy so a faked
  checker can't show green (depends on SR-F-3).

## Corrections / decision log

- 2026-05-17 — Doc created. v1 scope = SR-F-1..4 (policy + desired
  version + signed records). Commitment tokens, backup, status, audit =
  later. GitHub assumed legit; server is the local-tamper trust anchor.
  Daemon never talks to the server. Enforcement offline-complete /
  fail-closed.
- 2026-05-17 — SR-O-3 reframed as accountability-partner notification:
  it is a **social deterrent, not a technical control**; only lever for
  the determined-uninstall case the server otherwise can't touch. Hard
  problem = tamper-vs-offline noise (SR-Q-7); heartbeat is a weak signal
  until signed (SR-Q-8). Phase: later.
