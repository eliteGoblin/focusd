# ADR-0015 -- Platform-fetch must back off and decouple from the 2s mesh-heal cadence

- **Status:** proposed (2026-06-16) — known issue captured at deploy; fix not yet built
- **Severity:** HIGH (auto-update path only; steady-state is unaffected)
- **Relates to:** [FEATURE 10](../features/10-mesh-label-decorrelation.md) (the ~2s heal
  cadence), the path-rotating self-update path, and release-asset resolution
- **Found by:** Frank during the FEATURE 10 live deploy (PR #50)

## Context

FEATURE 10 tightened the self-heal loop to roughly every 2 seconds so a single
manual mesh removal loses the race. That cadence is correct for keeping the mesh
alive. But the same fast loop also drives the path that **fetches the protection
engine binary** when that binary is absent — a fresh install, a version change,
or after the engine crashes.

When the engine binary is missing, every ~2s tick tries to resolve the release
asset from GitHub. Unauthenticated GitHub API calls are capped at **60 per
hour**. At a 2s cadence the cap is exhausted in roughly two minutes, after which
release resolution returns **HTTP 403**. The engine then **cannot download and
stays down** — the protection layer is offline until the rate limit resets or a
human intervenes.

This only bites when the engine binary is *absent*. In steady state, with the
engine running, there is **no fetch**, so the live system is unaffected — which
is why it surfaced only at deploy time, not in normal operation.

## Decision (recommended direction — not yet built)

The engine-fetch path must stop hammering the API. Recommended, in order of
preference / combinable:

- **Back off on failure.** The fetch path should retry with exponential backoff,
  not on every 2s heal tick. A failed resolve must widen the gap between attempts.
- **Decouple fetch from heal cadence.** The fast 2s loop should keep the mesh
  alive; acquiring the engine binary should run on its own, slower schedule so
  tightening the heal loop never multiplies API traffic again.
- **Reduce dependence on the live unauthenticated API.** Consider an
  authenticated fetch (higher cap) and/or caching the resolved asset so a known
  version isn't re-resolved from scratch on every miss.

## Workaround used at this deploy

During the FEATURE 10 deploy this was sidestepped by placing the signed engine
binary **directly in the workdir**, so no fetch was needed. That is a manual
workaround, not a fix — a future fresh install or post-crash recovery would hit
the same 403 wall.

## Consequences

- Until fixed, the auto-update / cold-acquire path is fragile: a fresh install,
  a version bump, or an engine crash can drive the API to 403 and leave
  protection down.
- The manual pre-populate workaround must be remembered for any deploy that
  changes the engine version.
- Fixing this removes a self-inflicted outage mode introduced (made faster) by
  FEATURE 10's tighter cadence.

## References
- Heal cadence: [FEATURE 10](../features/10-mesh-label-decorrelation.md), ADR-0014
- Deploy mechanics: register §8
