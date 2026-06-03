# ADR-0013 -- Exactly one platform per install, enforced by a daemon-held lock

- **Status:** accepted (2026-06-01)
- **Supersedes:** the "platform grabs the lock" model in the self-protecting
  reconcile design doc (`app_mon/documents/design/self_protecting_reconcile_platform.md`).
  That doc described the platform locking itself; that was never built, and this
  ADR consciously moves the lock to the daemon instead. (Recommend the design
  doc be updated to match.)
- **Decided by:** architect (delegated by Frank, product owner) + BA review

## Context

Since the single-mesh decision (ADR-0010, FEATURE 8), the two launchd roles in
the mesh each run their own reconcile loop, and each one independently starts
its own platform process. There is no guard against both doing so at once. The
result on a perfectly healthy install: **two platform processes running against
the same workdir and the same state store** -- which means every protection
runs twice and the two processes contend over the shared database.

This was not visible until `daemon status` (ADR-0012) surfaced the live process
count and showed two platforms where there should be one. The original design
always intended a single-instance guard; it simply was never implemented.

## Decision

**Enforce exactly one platform per install via a crash-safe, OS-level advisory
lock -- and place that lock in the DAEMON, not in the platform.**

- Before a daemon starts its platform child, it must acquire the lock. The
  daemon that wins supervises the one and only platform. A daemon that loses the
  lock simply starts nothing and stays a warm standby.
- The lock is tied to the holder's lifetime: if the holder dies (crash, force
  kill), the OS releases it automatically. The standby's next reconcile tick
  then acquires it and brings the platform back up -- so the single-mesh
  self-healing property (ADR-0010) is preserved, now with no double-launch.

**Why daemon-side and not platform-side:** the daemon judges a child's health
by how long it stays up -- a child that exits very quickly is read as "crashed
on startup" regardless of why it exited, and a few quick exits in a row cause
the daemon to mark that release bad and roll back. If the platform locked
itself, the *losing* platform would exit immediately and be misread as a crash,
triggering a false rollback of a perfectly good release. With the lock in the
daemon, the loser launches no child at all -- no phantom exit, no false
rollback.

**Cross-platform shape:** the lock is expressed as a single port (one
interface) with one adapter per operating system, all built on the dependency
the project already carries. This is deliberately not a new third-party
locking library -- the interface is needed regardless, it reuses an existing
dependency, and it follows the project's established per-OS adapter pattern.
This is the **first worked example** of the "cross-platform Go, interface at the
OS seam" principle (register Section 5).

**Ships in:** the daemon line, daemon-v0.5.x. Daemon-layer change only; the
platform binary is unchanged.

## Consequences

- One platform per install: protections run once, no database contention,
  matching the original intent.
- Self-healing is intact -- standby promotes itself when the holder dies.
- No false rollbacks: the loser never produces a quick-exit the crash detector
  could misread.
- Establishes the OS-seam interface pattern in real code for future
  non-portable primitives to follow.

## Honest limitation

Only macOS actually double-launches today, because the launchd mesh is
macOS-only. Windows and Linux ship the lock for correctness and future
readiness, but they have no mesh yet, so there is nothing for it to deduplicate
on those platforms right now.

## References
- Single-mesh lineage: `0010-single-mesh-fail-fast.md`
- Status command that surfaced the double-launch: `0012-status-delegates-to-platform.md`
- OS-seam principle: `../REQUIREMENTS_REGISTER.md` Section 5
- Superseded design doc: `app_mon/documents/design/self_protecting_reconcile_platform.md`
