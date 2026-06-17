# Feature 13 — Heartbeat + accountability-partner alerting (the external/social layer)

- **Status:** 💡 proposed (icebox · 2026-06-17) — product owner is excited; **not**
  scheduled for build. Captured at product altitude for a future decision.
- **Maturity:** [exploring]
- **Related:** the **detection/alerting half** of the
  [icebox: server-managed enforcement mode](../icebox.md#server-managed-enforcement-mode-server-owns-the-commitment)
  (that entry owns the *off-box policy/loosening* half; this feature owns the
  *notice-and-tell-a-human* half).
- **Complements (does not replace):** [FEATURE 8](08-single-mesh.md) (in-band mesh) ·
  [FEATURE 12](12-out-of-band-watchdog.md) (out-of-band local watchdog). Those are
  the **local** friction; this is the **external/accountability** layer.

## Why (the gap it fills)

Every **local** protection layer is friction, not a wall. A determined sudo user
can eventually wipe the mesh **and** the out-of-band watchdog atomically and keep
doing it (this is stated honestly in FEATURE 12's limitations). And a server
**cannot restart a wiped client** — nothing can force software back onto a machine
where the user has root.

But a server **can** do the one thing no local layer can: **notice that protection
went down, and tell a human.** That converts "weak-moment-me silently kills it and
plays" into "weak-moment-me kills it and my accountability partner is alerted
within minutes." For an addiction commitment device, the **social/accountability
loop** is the durable deterrent that local software cannot provide on its own. The
product owner flagged this as **"very important for addicted people."**

## What (product owner's sketch)

- The running protection (the platform — it is the auth-aware layer) emits a
  **periodic, authenticated heartbeat** to a server. Most likely shaped as a
  dedicated plugin, which fits the existing plugin model.
- The server tracks each protected **device's** status: last-seen, up/down, and
  optionally a recent health/log summary.
- If the server **stops receiving heartbeats** from a device (agent killed, wiped,
  or offline) beyond a threshold, it flags that device **DOWN** and **alerts the
  accountability partner**.
- The accountability partner has an **app/dashboard** showing the live status of
  each protected device (and the person's), with the ability to drill into recent
  health/log detail.

The key semantics: **dead-man's switch.** Health is signalled by the *presence* of
a signed heartbeat; the *absence* of one is itself the down signal. There is
nothing for a wiped client to actively "fail" — silence is the alarm.

## How it behaves (product rules)

- **Healthy = visibly up.** While protection is running, the server shows the
  device "up / last-seen <recent>".
- **Down = a human hears about it.** If heartbeats stop past the threshold, the
  server marks the device DOWN and notifies the accountability partner within a
  bounded window (minutes).
- **Blocking the path still triggers the alert.** If the user firewalls the
  heartbeat so it can't reach the server, the server simply stops hearing it — and
  marks the device DOWN. Blocking the channel *is* the down signal, not a bypass.
- **A wiped client can't fake "I'm fine."** Heartbeats are authenticated, so a
  removed or tampered agent cannot forge a healthy signal to keep the partner calm.

## Acceptance sketch (testable behaviour, product-level)

1. While protection is healthy, the server shows the device **"up / last-seen
   <recent>"**.
2. If the agent is killed/wiped and heartbeats stop, the server marks the device
   **DOWN** and **notifies the accountability partner within a bounded window
   (minutes)**.
3. The partner's app lists **all linked devices** with current status and can view
   recent health/log detail.
4. A wiped or tampered client **cannot spoof a healthy heartbeat** — absence of a
   valid signed heartbeat is itself the DOWN signal (dead-man semantics).

## Honest limitations (record; do not over-claim)

- **It detects + alerts; it does NOT restart the client.** The server cannot reach
  a dead agent to bring it back. This is a notification/accountability layer, not a
  remote-recovery layer — be explicit about this boundary.
- **It needs real infrastructure.** A server, device enrollment + authentication
  (so heartbeats are trustworthy and a wiped client can't forge them), and a
  partner-facing app/dashboard. This is materially more than the current
  single-machine product shape.
- **The deterrent is social, not technical.** Its power comes from a real human
  caring and being told. If the partner ignores alerts, the layer delivers nothing.
- **Local friction still matters.** This complements — it does not replace — the
  mesh + out-of-band watchdog. The few-minutes-of-freedom problem before an alert
  lands is real; local friction is what holds the line in those minutes.

## Design questions (resolve before promoting to build)

- **Privacy: what is transmitted?** Status-only, or log excerpts too? What does the
  partner get to see, and what is the person comfortable sharing? **Open product
  question — flag for the human.**
- **Partner consent + relationship.** How does someone become an accountability
  partner, how do they accept, and how is the link revoked? Is revoking the link
  itself a weak-moment escape hatch that needs its own friction?
- **Who is the partner persona?** This introduces a brand-new persona (the
  off-box accountability holder) the current threat model does not have. **Scope
  expansion — needs human sign-off.**
- **Alert threshold + channel.** How long of a silence counts as DOWN (balancing
  false alarms on laptops that sleep/travel vs. fast detection)? What channel
  (push, SMS, email)?
- **Where does the server live + how is it kept un-nukeable** by the weak-moment
  self? (Shared with the server-managed-enforcement entry — an on-box server gives
  no accountability value.)
- **Device enrollment + auth.** How devices bind to the server so heartbeats are
  trustworthy and a wiped client can't re-enroll a fake one. (Shared dependency
  with the server-managed-enforcement entry.)
- **Relationship to enforcement mode.** This is the detection/alerting half of the
  off-box layer; the loosening/policy half lives in the icebox entry. Decide
  whether they ship together or this lighter detection-only slice can ship first.
