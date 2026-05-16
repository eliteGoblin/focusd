# focusd platform

Cross-platform protection platform — orchestrator core + OS adapter +
separately-released executable plugins. macOS first, Windows later.

Refactor of the `app_mon` v0.6.1 monolith. The monolith is left
**untouched**; this tree lives alongside it (see
`requirements/support_plugin_platform_refactor/platform_refactor_plugin.md`).

## Layers

| Layer | Responsibility |
|-------|----------------|
| **core** | config, plugin discovery/validation, scheduler, runner, state, logging — the orchestrator. No OS-specific code. |
| **osadapter** | the *only* OS boundary: run-mode detection, path layout, agent lifecycle (later). `adapter_{darwin,windows,linux}.go`. |
| **plugins** | separately released external binaries. Platform execs them; never Go `.so`. |

```
platform/                    plugins/
  cmd/platform/main.go         kill-steam/
  internal/                      cmd/main.go
    core/{config,plugin,           internal/killer/
      runner,scheduler,            plugin.json
      state,logging,app}         go.mod  (independent module)
    osadapter/                 scripts/{build-platform,build-plugins}.sh
    integration/               go.work  (ties modules for local dev)
```

## Run modes (fully isolated)

`user` (no admin; managed laptops) and `system` (root; stronger). The two
use **separate** runtime roots and never touch each other. A forced
`system` mode without privilege fails fast — no silent downgrade.

macOS layout: `~/Library/Application Support/focusd/` (user) or
`/Library/Application Support/focusd/` (system), each with
`config.yaml`, `state/state.db`, `plugins/`, `logs/platform.log`.

## Build & run

```bash
# from repo root
bash scripts/build-platform.sh      # dist/focusd-platform-<os>-<arch>
bash scripts/build-plugins.sh       # dist/<plugin>/ (plugin.json+bin+checksums)

platform validate [--config P] [--state-db P] [--plugin-dir D] [--mode user|system]
platform run      [...]             # starts scheduler, SIGINT/SIGTERM = graceful drain
```

State is SQLite via `modernc.org/sqlite` (no CGO ⇒ trivial
cross-compile). Desired config is YAML; observed state is SQLite only.

## Plugin contract

```
<binary> run --config <resolved-job-config.json>
stdout = {"status","message","details"}   stderr = diagnostics
exit 0 = ok · 1 = controlled failure · 2+ = runtime error
```

`plugin.json` manifest declares `protocol_version`, `supported_os/arch`,
`required_privilege`, `run_as`. Discovery rejects (and records, with
reason) unknown protocol, wrong host, privilege mismatch, or — in system
mode — plugins from user-writable directories.

Job execution: temp-file config, context timeout (process-group kill),
retry on error/timeout only, no-overlap via `job_locks`, full history in
`job_runs`.

## Plugins

- **kill-steam** (user) — terminates Steam/Dota2 by exact process-name
  match (ports app_mon policy incl. the v0.6.1 #17 msteams fix).
- **browser-monitor** (planned) — user-mode browser-tab guard.

## Testing

```bash
cd platform && go test ./...           # all core packages ≥80%
cd plugins/kill-steam && go test ./...  # ≥80%
```

Tests never require root: process-killing is tested via seams and
non-matching names; the integration test builds the real plugin and
drives it through the real runner/state. Spec scenarios (success,
controlled-failure, runtime-error, timeout, invalid-json, stderr-heavy,
unsupported-os, system-required-under-user) are all covered.
