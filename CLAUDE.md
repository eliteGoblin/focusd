# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Project Overview

**FocusD** is a multi-layer defense system against addictive apps and websites on macOS.

**Layers:**
1. **DNS Blocking (NextDNS)** - Network-level domain blocking for all devices
2. **App/Site Blocking (daemon + platform + plugins)** - A self-protecting
   supervisor keeps a cross-platform protection engine running; the engine
   enforces blocking policy through plugins (process-killing, host/packet
   blocking, Claude-skill re-injection)

## Key Components

The active system is a Go workspace (`go.work`) of three cooperating layers.

### daemon (Layer-1 supervisor)
Keeps the platform alive and protects itself: a single mutually-respawning
launchd mesh plus path-rotating self-update. **Plugin-agnostic by design
("daemon-thin")** — it supervises the platform and reports status, but knows
nothing about individual plugins.
- Location: `daemon/`

### platform (protection engine)
Cross-platform Go engine that drives the running system toward a **signed
desired state** through one idempotent reconcile loop. Runs the plugins on
schedule, with runtime privilege-drop for user-domain jobs.
- Location: `platform/`

### plugins (enforcement units)
Independent Go modules the platform invokes as scheduled jobs:
- `dns-block` - reconciles the blocklist into the hosts file
- `kill-steam` - kills Steam/Dota2 processes
- `network-block` - pfctl packet filtering via DoH
- `skill-protector` - re-injects the Claude refusal skill/rule
- `browser-monitor` - kills browsers sitting on a blocklisted tab
- Location: `plugins/`

### Requirements (product/BA docs)
Feature specs, ADRs, and the register — the source of truth for what/why.
- Location: `requirements/` (`features/`, `decisions/`, `REQUIREMENTS_REGISTER.md`)

### NextDNS Setup Guide
DNS-level blocking setup for router + all devices.
- Location: `artifacts/managed_dns/next_dns.md`

### Chrome Focus (Deprecated)
Chrome extension enforcer - archived, no longer maintained.
- Location: `archive/chrome/`

## Project Structure

```
focusd/
├── daemon/          # Layer-1 supervisor (Go) — keeps platform running, self-protecting mesh
├── platform/        # Cross-platform protection engine (Go) — reconcile spine, runs plugins
├── plugins/         # Enforcement plugins (Go): dns-block, kill-steam, network-block,
│                    #   skill-protector, browser-monitor
├── requirements/    # Product/BA docs: features/, decisions/ (ADRs), REQUIREMENTS_REGISTER.md
├── artifacts/
│   └── managed_dns/next_dns.md   # NextDNS setup guide
├── scripts/         # Build/bundle helpers (e.g. build-platform.sh)
├── archive/chrome/  # Deprecated Chrome extension enforcer
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

## Important Notes

- **No stop command**: intentional friction to prevent impulsive disabling
- **Self-protection**: the daemon runs a single mutually-respawning launchd mesh
  and path-rotates on self-update
- **Single platform**: a daemon-held advisory lock keeps exactly one platform
  supervisor running (ADR-0013)
- **Signed desired state**: the platform only moves toward a signed policy;
  enforcement is tighten-only ("no inside door handle")
- **Legacy removed**: the original single-binary `app_mon` implementation was
  superseded by daemon/platform/plugins and removed (recoverable from git history)
