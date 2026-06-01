# ADR-0012 — `daemon status` delegates plugin detail to the platform (KISS layering)

- **Status:** accepted (2026-06-01)
- **Supersedes:** the status-probing portion of the prior Feature 09 design
  (where `daemon status` probed everything itself, including reaching into the
  platform's state store). The redaction contract (ADR-0011) is unchanged.
- **Decided by:** Frank (product owner) + BA review

## Context

The earlier Feature 09 design had `daemon status` probe the whole system
itself — engine, every protection's recency, blocklist, packet filter, skill
files — which meant the daemon reaching into the platform's state store and
effectively knowing that plugins exist.

The product owner's call, mid-build: *"daemon should KISS, know least of
dependencies unless strictly having to ... I don't want daemon have to know
plugin, it should not depend on it."* A daemon coupled to plugin internals and
the platform DB is more moving parts, a harder boundary to reason about, and
the same class of "implemented one way, I only caught up later" surprise the
single-mesh decision (ADR-0010) already pushed back on.

## Decision

**Split status along the module seam. The daemon stays plugin-agnostic.**

1. `daemon status` reports **only daemon-owned facts**: how many launchd mesh
   roles are running, whether the platform process is up, and the platform
   version (desired vs last-known-good).

2. **The platform owns plugin/protection detail** — per-protection last result
   + recency, blocklist size, packet-filter size, skill files — surfaced by a
   new `platform status`. It emits only non-disguised primitives (job ids,
   statuses, coarse age buckets, counts).

3. `daemon status` **delegates** to `platform status` and **passes its output
   through**, so the operator still sees one combined snapshot. The daemon
   never reads the platform's state store and never knows a plugin exists.

## Consequences

- **Clean module boundary:** no daemon → state-DB coupling, no daemon → plugin
  coupling. The daemon knows the mesh and the platform process; nothing more.
  Matches the standing "daemon thin / no plugin dep" principle.
- **Reinforces redaction (ADR-0011):** mesh labels are counted inside the OS
  adapter and only counts cross the seam; disguised strings have less surface
  to leak through.
- **Trade — accepted:** per-protection recency is now a **last-run-status
  proxy** reported by the platform, not a live re-probe the daemon performs at
  status time. Slightly less immediate; judged a worthwhile cost for the
  thinner, plugin-agnostic daemon.
- If the platform process is down, the daemon still reports its own facts and
  marks platform detail unavailable, rather than failing.

## References
- Feature spec: `../features/09-status-command.md`
- Redaction ADR: `0011-status-redaction.md`
- Single-mesh / KISS lineage: `0010-single-mesh-fail-fast.md`
