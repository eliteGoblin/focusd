# Shipment / Release — Design (far-future tracker)

**Status: NOT STARTED. Tracking only.** KISS. Companion to
[README.md](./README.md) · [daemon_design.md](./daemon_design.md).

> **Now:** NOT shipped via the Mac App Store and NOT Apple-notarized.
> Plain signed binary, our own key. The design must allow a **later,
> far-future migration to Apple notarization / Team ID / App Store**
> without reworking peer-recognition. That migration is additive, not a
> rewrite.

## Locked decisions

- **Recognition signing = our own Ed25519 key** (D14). Private key lives
  only in the release pipeline (offline); public key embedded in the
  binary. OS-portable; behind the OS interface.
- Apple notarization / Team ID, **if/when** macOS distribution happens,
  is a **separate, additive** release step for Gatekeeper/install trust
  — **not** the peer-recognition mechanism. Recognition stays the
  Ed25519 check on every platform.
- Recognition proves "genuine, unmodified appmon build"; it does not
  hide and does not prove runtime behaviour (honest ceiling unchanged).

## Signature placement (pick at build time)

| Option | Shape | Use when |
|---|---|---|
| **A** sidecar | `appmon` + `appmon.sig` | simplest |
| **B** appended trailer | single self-contained binary; self-verifies via `os.Executable()` | "user has one binary" (preferred) |
| **C** signed manifest | `manifest{file:sha256,version}` + `manifest.sig` | signing daemon + platform + plugins together |

The signature is **always produced post-`go build`** by the pipeline
(a file cannot contain its own signature). Runtime is pure file-read +
`crypto/ed25519.Verify` — no binary patching at runtime.

## Release pipeline (functional — RS-F)

| ID | Requirement | Phase |
|----|-------------|-------|
| RS-F-1 | Build per OS/arch (CGO-free where possible). | when daemon ships |
| RS-F-2 | Post-build: sign with offline Ed25519 private key (Option B or C). | with RS-F-1 |
| RS-F-3 | Publish versioned artifact + signature to GitHub Releases (HTTPS; assumed legit). | with RS-F-1 |
| RS-F-4 | Embed matching **public key** + key id/version in the binary (key rotation possible). | with RS-F-1 |
| RS-F-5 | Idempotent installer: detect existing install (structural), update in place; no duplicate/garbage. | daemon phase |
| RS-F-6 | Apple notarization / codesign for macOS distribution. | far future, additive |
| RS-F-7 | Mac App Store packaging/sandbox review. | far future, optional |

## Non-functional (RS-N)

- RS-N-1 Private key never on a user machine, never in the binary, never in CI logs.
- RS-N-2 Key rotation supported (embed key id; binary can carry >1 trusted public key during rollover).
- RS-N-3 Reproducible-ish builds preferred (so a signature maps to known source).
- RS-N-4 Release mechanism identical across OSes for recognition; macOS Apple-signing is the only platform-specific *additive* step.

## Open questions (RS-Q)

- RS-Q-1 Option B vs C (single self-verifying binary vs signed manifest for multi-artifact).
- RS-Q-2 Ed25519 key storage (HSM / cloud KMS / offline file) + rotation cadence.
- RS-Q-3 Whether the daemon and platform share one key or have separate keys.
- RS-Q-4 Far-future: Developer ID vs App Store distribution for macOS; sandbox impact on the daemon's launchd self-protection.

## Corrections / decision log

- 2026-05-17 — Doc created. Now: own-Ed25519 signed binary, no Apple
  involvement. Apple notarization/Team ID = far-future additive step,
  separate from recognition. Recognition mechanism is OS-portable and
  must not depend on Apple.
