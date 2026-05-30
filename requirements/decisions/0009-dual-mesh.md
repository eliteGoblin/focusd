# ADR-0009 — Dual mesh (two protection engines)

- **Status:** superseded by [ADR-0010](0010-single-mesh-fail-fast.md) on 2026-05-30
- **Decided by:** implementation choice during FEATURE 1.6

## Context

The behavioural protection (re-injecting the Claude refusal skill into the
user's home directory) must run as the **user**, because the files it writes
must be owned by the user. The other protections (blocking sites, killing the
game, packet filtering) need **admin** rights. A single admin engine refused to
run the user-level task, so a **second, user-level engine** was installed
alongside the admin one to host it.

## Decision (at the time)

Run two protection engines simultaneously — one admin, one user — each hosting
the protections that match its privilege level.

## Why it was superseded

It worked but wasn't KISS: two engines, two state stores (which drift), two
install footprints, and a doubled identity that a future server would have to
reconcile for a single person. Replaced by a single engine that temporarily
steps down to the user's identity for the one user-level task. See ADR-0010.

## Kept for history
This record stays so the dual-engine approach isn't re-proposed without
remembering why it was set aside.
