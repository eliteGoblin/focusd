# ADR-0014 -- Independent per-role mesh labels with a masked workdir roster

- **Status:** accepted (2026-06-15)
- **Supersedes:** the "accepted for now" shared-prefix + role-suffix mesh-label
  stance (the deferred trade-off tracked as GitHub issue #20).
- **Feature:** [FEATURE 10](../features/10-mesh-label-decorrelation.md)
- **Decided by:** Frank (product owner), user-locked + BA review

## Context

The self-protection mesh is three launchd entries: two always-on workers that
respawn each other and one periodic ensurer (the backstop). Today all three
share **one** random base name and differ only by a role suffix
(`.a` / `.b` / `.ensure`). Two weaknesses follow:

- **Shared prefix is a cluster-find.** Discovering any one entry reveals the
  common prefix; a single grep on that prefix returns all three. One discovery
  unravels the whole mesh.
- **The suffix advertises the structure.** `.a` / `.b` / `.ensure` tells the
  reader this is a coordinated mesh and which entry is the backstop.

Separately, the heal loop reconciles every ~10s, while a person can remove the
three entries one at a time in ~5s total — so today **manual removal wins the
race**.

This was a known, deferred trade-off (issue #20). With the single mesh
(ADR-0010) and the singleton lock (ADR-0013) settled, it is time to close it.

## Decision

Switch from one-shared-base-plus-role-suffix labels to **independent,
fully random labels per mesh role**, persist the roster in a **XOR-masked**
workdir file, and **tighten the reconcile cadence ~10s → ~2s**.

- **Independent labels.** Each role draws its own prefix + suffix + tail from
  the existing disguise pool. No shared base; no role-revealing token. The
  three read as unrelated third-party agents.
- **Masked roster, memory-led.** The roster is written to the hidden workdir,
  masked with the **same casual-grade XOR pattern already used for the embedded
  pubkey** (FEATURE 3). In-memory roster is authoritative; the file is cold-start
  recovery only and self-heals from memory if edited or deleted.
- **Faster heal.** A single removed entry is recreated in ~2s, inside the manual
  one-at-a-time removal window.

## Alternatives considered

- **Keep the shared base, just drop the `.a/.b/.ensure` tell.** Rejected: it
  removes the role advertisement but leaves the shared-prefix cluster-find
  fully intact — the bigger of the two weaknesses.
- **Roster in plist args vs a single workdir manifest file.** A roster-in-plist
  approach embeds the cross-references in the launchd entries themselves;
  Frank chose the **simpler workdir file** — one place, reusing the existing
  mask, easy to self-heal from memory.
- **Real encryption instead of XOR mask.** Rejected as not-KISS and
  over-claiming: this layer is friction, not crypto; reusing FEATURE 3's mask
  keeps it honest and dependency-free.

## Consequences

- The three mesh labels are no longer correlatable by prefix, and no label
  reveals its role — the cluster-find and the backstop tell are both closed.
- Single-entry manual removal now loses the heal race.
- One more masked workdir artifact (the roster), self-healing and consistent
  with the existing mask pattern.
- Closes issue #20.

## Honest limitation

Casual-grade friction only. It defeats a casual `cat`/`ls`, the
`launchctl … | grep <prefix>` cluster-find, and slow manual removal. It does
**not** stop reading the daemon binary to recover the XOR key, invoking the
daemon's own un-mask path, or a scripted **atomic** bootout+rm of all three at
once. Decorrelation raises the cost of *finding* the set, not of removing a set
already held. The durable commitment weight stays in the server-side override
gate.

## References
- Single-mesh lineage: `0010-single-mesh-fail-fast.md`
- Singleton lock (prior mesh-layer ADR): `0013-platform-singleton-daemon-flock.md`
- Reused mask pattern: FEATURE 3 (pubkey grep-resistance), register §4
- Cross-platform OS-seam principle: `../REQUIREMENTS_REGISTER.md` Section 5
- Closed trade-off: GitHub issue #20
