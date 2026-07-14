# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Project Overview

**FocusD** is a layered supervision system against addictive apps and websites on macOS, built on a principle: **each layer protects the one below it, preventing tampering through continuous verification.**

**Architecture:**
1. **Daemon (Layer 1 supervisor)** - Keeps the platform running and verified; self-protecting mesh with out-of-band recovery
2. **Platform (Layer 2 protection engine)** - Runs plugins on schedule; verifies each plugin before execution
3. **Plugins (Layer 3 enforcement units)** - Kill processes, block hosts/packets, re-inject skills, monitor browsers

Each layer Ed25519-verifies the layer beneath it before execution. A tampered plugin is restored; a tampered platform is restored.

## Key Components

The system is a Go workspace (`go.work`) with three supervision layers.

### daemon (Layer 1: supervisor + anti-tamper)
Ensures the platform is **alive** AND **genuine**. Keeps the platform running via a self-protecting mutually-respawning launchd mesh and **Ed25519-verifies the platform before every execution** — if the platform binary is modified, it's restored from a signed source before it runs. **Plugin-agnostic by design ("daemon-thin")** — supervises the platform, doesn't know about plugins. Features:
- Mutually-respawning 3-job mesh (defeats single-process kill)
- Path-rotating self-update (fresh location each upgrade)
- Out-of-band launchd companion for recovery when main mesh is down
- Baked fallback platform version (recovery when network unavailable)
- Single-instance enforcement (exactly one platform + one daemon generation)
- Location: `daemon/`

### platform (Layer 2: protection engine + anti-tamper)
Runs the plugins on an idempotent reconcile loop. **Continuously re-verifies each plugin's authenticity before execution** — a swapped plugin is detected and restored from the embedded signed copy before it runs. Drives the system toward a **signed desired state**; enforcement is **tighten-only** (config can only add restrictions, never disable existing ones).
- Plugin scheduling and result collection
- Plugin binary integrity verification (Ed25519-verified, embedded in platform binary)
- Tamper detection and auto-restore
- Per-plugin atomic binary swap-out (if modified, restore genuine before next run)
- Atomic status snapshot reads (prevents false-green from concurrent access)
- Location: `platform/`

### plugins (Layer 3: enforcement units)
Independent Go binaries the platform runs as scheduled jobs, each Ed25519-signed and embedded in the platform binary:
- **kill-steam** — Kills Steam/Dota2 processes; removes app+game data each run
- **dns-block** — Writes `/etc/hosts` blocklist for gaming domains
- **network-block** — pfctl packet filtering for Steam IP addresses (optional, system-mode only)
- **skill-protector** — Re-injects Claude refusal skill if user deletes it
- **browser-monitor** — Monitors and quits browser windows on blocklisted sites
- **freedom-protector** — Keeps Freedom focus app alive (requires license; user-mode optional)
- Location: `plugins/`

### Requirements (product/BA docs)
Feature specs, decisions (ADRs), and requirements register — the source of truth for what each feature defends against and known limitations.
- Location: `requirements/` (features, decisions, REQUIREMENTS_REGISTER.md, e2e-test-history.md)

### NextDNS Setup (Optional)
Router-level DNS blocking setup for all devices on your network. *Optional*; `dns-block` plugin already blocks at `/etc/hosts` level.
- Location: `artifacts/managed_dns/next_dns.md`

### Archive (Deprecated)
Chrome extension enforcer — superseded by browser-monitor + Freedom. No longer maintained.
- Location: `archive/chrome/`

## Project Structure

```
focusd/
├── daemon/          # Layer 1: supervisor + anti-tamper
│   ├── cmd/daemon/          # Main daemon binary
│   ├── internal/osadapter/  # macOS launchd, plist, process helpers
│   ├── internal/platformsvc/# Platform liveness + recovery
│   └── ...
├── platform/        # Layer 2: protection engine + anti-tamper
│   ├── cmd/platform/        # Main platform binary
│   ├── internal/core/       # Reconcile loop, plugin runner, status
│   ├── internal/bundle/     # Embedded + bundled plugins
│   └── ...
├── plugins/         # Layer 3: enforcement units (each Ed25519-signed + embedded)
│   ├── kill-steam/
│   ├── dns-block/
│   ├── network-block/
│   ├── skill-protector/
│   ├── browser-monitor/
│   └── freedom-protector/
├── requirements/    # Product/BA docs
│   ├── features/            # Feature specs (FEATURE 1-20+)
│   ├── decisions/           # ADRs (ADR-0001, etc.)
│   ├── REQUIREMENTS_REGISTER.md  # Master index + threat model
│   └── e2e-test-history.md  # Live e2e results + verification matrix
├── artifacts/
│   └── managed_dns/next_dns.md   # Optional: router-level DNS setup
├── scripts/         # Build/test helpers
│   ├── build-platform.sh    # Build platform + bundle plugins
│   └── testmode/            # E2E test harness
├── archive/         # Deprecated
│   └── chrome/              # Old Chrome extension enforcer
├── go.work          # Go workspace (daemon + platform + plugins)
└── CLAUDE.md        # This file
```

## Technology Stack

- Language: Go 1.25 (workspace via `go.work`)
- CLI: Cobra · Logging: Zap · Testing: testify + Ginkgo
- CGO-free (`modernc.org/sqlite`) for trivial cross-compilation
- Ed25519 release signing; binaries verified before any swap

## Common Commands

```bash
# Build the platform (bundles plugins first)
./scripts/build-platform.sh

# Build / test a module (workspace)
cd daemon   && go build ./... && go test ./...
cd platform && go build ./... && go test ./...
```

## Important Shipped Features

### Anti-tamper: Layered supervision + Ed25519 verification
- **Daemon→Platform**: Daemon Ed25519-verifies the platform binary before every execution. A tampered/swapped platform is restored from the signed source before it runs.
- **Platform→Plugins**: Platform continuously re-verifies each plugin's authenticity before every run. A tampered plugin is restored from the embedded signed copy (bundled in the platform binary) before execution.
- **Principle**: "Daemon guards the platform; platform guards the plugins."

### Recovery resilience (FEATURE 17-19)
- **Baked fallback**: Platform version embedded in daemon binary so a wiped workdir can recover offline
- **Out-of-band companion** (FEATURE 18): Separate launchd agent keeps signed daemon backup; recovers daemon if main mesh is down
- **Single-instance convergence**: Install/upgrade retires old generations, converges to exactly one daemon + one platform
- **Orphan reaper**: Automatically removes stale generations from prior installs

### Disguise (FEATURE 19)
- **Workdir off argv/env**: Working directory derived from binary location, not passed on command line — invisible to `ps` / `ps aux`
- **Neutral process names**: Mesh labels are randomized, varied, non-clustering — defeats grep-for-pattern discovery
- **Mesh role/marker hidden**: Role and mesh indicators moved off argv into plist environment variables

### Configuration: Tighten-only enforcement
- Config overrides can **only add** restrictions, never disable existing protections
- A signed desired state is the enforcement target; the platform only moves toward tightening

### No stop command
- Intentional friction; only sanctioned removal path is the multi-step, multi-hour override ritual
- Prevents impulsive disable via `daemon stop` or similar

### Single platform instance (ADR-0013 + FEATURE 25)
- Daemon-held advisory lock ensures exactly one platform supervisor runs
- Prevents plugin-run duplication and DB contention

## Architecture Notes

- **Daemon-thin principle**: Daemon is plugin-agnostic by design — it supervises the platform and knows nothing about individual plugins. All plugin logic is in the platform.
- **Signed releases**: All binaries (daemon, platform, plugins) are Ed25519-signed. Keys embedded in the daemon; binaries verified before execution.
- **No hardcoded paths**: All paths derived deterministically from the binary location or OS-specific layouts (e.g., `~/Library/...`). No command-line paths that could leak in process listings.

## Known Limitations

- **Friction, not impossibility**: A root user can eventually defeat all local protections by removing all copies of the daemon + wiping all state locations. The system is designed to make this sufficiently inconvenient that an *impulsive* user-at-a-weak-moment will fail. Durable locks require an off-box layer (future: server-side heartbeat + accountability partner alerts).
- **Hardening in progress**: Features HF1-HF4 (storage separation, recovery, plugin continuous authenticity, deeper disguise) are on branch `hardening/stage1-consolidated`, not yet merged to master. These are test-mode verified but not live-verified on master yet.
- **Freedom-protector gaps**: Requires the user to install + license the Freedom app; doesn't prevent a user who disables blocking from within Freedom itself.
- **App-allowlisting environments**: On locked-down corporate Macs (e.g., ThreatLocker enforced), the full daemon/platform stack may not install; fallback is the utility-tier `mac-browser-guard` (user-mode only, browser-tab blocking only).
