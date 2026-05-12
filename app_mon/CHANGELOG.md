# Changelog

All notable changes to appmon will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.6.0] - 2026-05-12

### Features
- **DNS-layer blocking via managed `/etc/hosts` section.** appmon now
  installs a permanent ban list of hostnames as `0.0.0.0 <host>`
  entries inside `# BEGIN appmon-blocklist` / `# END` markers in
  `/etc/hosts`. Entries OUTSIDE the markers are preserved verbatim;
  entries INSIDE are reverted to the compiled-in list on every 60s
  watcher tick. This closes the major leak where Steam (which ignores
  the system HTTP proxy) could still resolve and reach
  `store.steampowered.com` during its brief launch window.
- **Quick-kill tick at 10 seconds.** Watcher gains a fast process-kill
  loop (`Enforcer.EnforceKillOnly`) in addition to the existing full
  scan. Closes the launch-to-kill window from up to 5 minutes (the
  prior `EnforcementInterval`) down to ~10 seconds. Heavy phases (brew
  uninstall + file delete) stay on the 60-second tick to keep CPU
  negligible.
- **`appmon blocklist` CLI command.** Prints the compiled-in
  permanent ban list and the active /etc/hosts list side-by-side,
  flagging divergence. Read-only — never modifies /etc/hosts. Works
  without sudo (just reads `/etc/hosts`, which is world-readable).
- **`EnforcementInterval` dropped 5 min → 60 s.** With quick-kill at 10s
  the heavy scan no longer needs to be the only process-killing line
  of defense, so it can run more often without CPU cost — that means
  brew-uninstall and path-deletion happen within a minute of a fresh
  Steam install rather than within five.

### Architecture
- New `internal/policy/dns_blocklist.go` — exports `DefaultDNSBlocklist`,
  a flat slice of ~68 hostnames covering the user's base sites plus
  subdomain expansion (`www.<d>`, `m.<d>`, plus Steam's CDN/API/store
  hostnames). To extend, edit this slice and rebuild — that's the
  permanent-ban semantic.
- New `internal/infra/hosts_manager.go` — `HostsManager.EnsureBlocklist`,
  atomic write via temp+rename, preserves 0644 mode, idempotent when
  on-disk content already matches. `FlushDNSCache()` invokes
  `dscacheutil` + `mDNSResponder` so changes take effect within
  seconds rather than on the resolver TTL.
- New `Enforcer.EnforceKillOnly` interface method — splits the fast
  kill phase out of the heavy `Enforce` for the quick-tick caller.
  `enforceKill` is the private helper both code paths share.
- `Watcher` gains a `hostsManager` field and an `ensureHostsBlocklist`
  method called on startup + the existing 60s plist-check tick. User
  mode degrades silently (EACCES → Debug log) since `/etc/hosts` is
  root-owned; DNS-layer blocking is system-mode-only.

## [0.5.3] - 2026-05-12

### Features
- **`appmon status` works without sudo now.** Liveness comes from `ps`
  (world-readable) via the new `infra.DetectLiveDaemons`. The encrypted
  registry is still queried for enrichment (heartbeat, daemon version)
  but is no longer required for the "is it running?" answer. Closes the
  gap where a user-mode CLI binary reading the user-mode registry would
  report NOT RUNNING while system-mode daemons were healthy.
- **`runStatus` is read-only.** Previously it would auto-create the
  encrypted registry's key/DB just to *check* status — silently
  recreating the user-mode footprint after cleanup and re-introducing
  the dual-mode trap. Now it probes `KeyProvider.KeyExists()` first and
  falls through to ps-only output when no registry exists for the
  invoking mode.
- **`appmon start` auto-purges other-mode artifacts.** Extends
  `detectAndCleanupOtherModeDaemons` (which previously only removed the
  other-mode plist) to also remove the other-mode binary
  (`~/.local/bin/appmon` or `/usr/local/bin/appmon`) and data dir
  (`~/.appmon` or `/var/lib/appmon`). Runs unconditionally, idempotent
  when state is clean. Prevents the dual-mode accretion that causes
  status-lies-to-user bugs.
- **Mode-mismatch hints in `status`.** When ps shows daemons running
  but the current CLI's registry is empty/unreadable, status prints a
  clear hint pointing to `sudo /usr/local/bin/appmon status` for full
  enrichment instead of silently lying with "NOT RUNNING".

### Architecture
- New `infra.LiveDaemon` struct + `infra.DetectLiveDaemons` — structured
  ps-based discovery returning PID/role/path tuples. Source of truth for
  liveness independent of which mode's registry the caller can read.
- Pure parser `parseLiveDaemons(string)` and `extractRoleArg(string)`
  split out for unit-testable filter logic without depending on
  real-system process state.
- `otherModePaths(execMode)` helper centralizes the canonical artifact
  paths for the "other" mode, used by the auto-purge step.

## [0.5.2] - 2026-05-12

### Features
- **`sudo appmon start` is now the universal recovery button.** It runs an
  unconditional pre-flight cleanup before respawning:
  - kills any `appmon`-named daemon process (legacy v0.5.0 ghosts from
    before relocation existed) via the new
    `infra.FindLegacyAppmonDaemons` helper;
  - kills any process running under the relocator cache dir whose PID
    isn't in the encrypted registry.
  The CLI's own PID is exempt; CLI invocations like `appmon status` are
  filtered out by the `daemon --role` argv check, so they're safe.
- **Heartbeat-aware liveness** in both `EncryptedRegistry.IsPartnerAlive`
  and `FileRegistry.IsPartnerAlive`. PID-running is necessary but no
  longer sufficient: peer-restart now also requires `last_heartbeat`
  within 2 minutes. This closes the stuck-but-alive class of bug — a
  deadlocked daemon that keeps its PID but stops heartbeating used to
  block peer-restart forever; now it gets respawned automatically.
- **Stale-heartbeat detection in `appmon start`**: the "already running"
  short-circuit now requires both PID-alive AND fresh heartbeat. A
  registered-but-stuck watcher no longer prevents `start` from doing
  recovery work.

### Bug Fixes
- **`status` "Auto-start: disabled" false positive**: previously
  `os.Stat`ed the plist path stored in backup config, which drifted
  whenever the randomized plist label rotated. Now uses
  `launchdManager.IsInstalled()` — same source of truth the watcher
  uses for self-heal — so status matches reality.

### Architecture
- New `infra.FindLegacyAppmonDaemons` (in `relocator.go`) — ps-based scan
  for pre-relocation daemons. Pure parser split out as
  `parseLegacyAppmonDaemons(string)` for unit-testable filter logic.
- `watcher.reapOrphans` now also matches legacy `appmon`-named daemons,
  not just relocator-dir PIDs. So the watcher's 60s tick eventually
  cleans up legacy ghosts even without an explicit `appmon start`.

## [0.5.1] - 2026-05-12

### Features
- **killall / pkill resistance** via binary relocation. Each daemon spawn copies
  or hard-links `/usr/local/bin/appmon` to a randomized system-looking basename
  (`com.apple.cfprefsd.xpc.<hex>` etc) under an obfuscated cache dir
  (`~/.cache/.com.apple.xpc.<host-hash>/`) and execs from there. The kernel's
  `p_comm` for the running daemon is the relocated basename, so
  `killall appmon` matches nothing. Path does not contain "appmon", so
  `pkill -f appmon` also misses. Each spawn rotates the name, so an attacker
  who learns one name can only kill that one instance — peer-restart spawns
  the partner under a new random name.
- **Login Items obfuscation**: LaunchAgent / LaunchDaemon plist now references
  a "launch stub" — a relocated copy of the main binary stored at a
  randomized path. macOS Login Items shows the obfuscated basename instead
  of `appmon`.
- **5-min cron-like respawn**: plist `StartInterval: 300` plus
  `KeepAlive: { Crashed: true, SuccessfulExit: false }` means launchd
  re-fires `appmon start` every 5 minutes. `start` is idempotent — fast no-op
  if both daemons are alive, full respawn if dead. Belt-and-suspenders backup
  to peer-restart in case both daemons die simultaneously.
- **Daemon self-relocate fallback**: `appmon daemon …` re-execs itself from a
  relocated path on startup if exec'd from outside the relocator dir
  (e.g. by an older parent during an update). Idempotent.
- **Orphan reaper**: Watcher periodically (on startup + every 60s) scans
  the relocator cache dir and SIGKILLs any process whose PID is not in the
  encrypted registry. Makes the registry the single source of truth for
  daemon membership and self-heals after failed updates, racing spawns, or
  any stale state.

### Bug Fixes
- **CRITICAL — updater registry split-brain**: `NewUpdater` previously
  instantiated `NewFileRegistry` (the legacy JSON file at
  `/var/tmp/.cf_sys_registry_*`) while the live daemons write to the
  encrypted SQLCipher registry. Health checks therefore never saw the just-
  spawned daemons, every update timed out and triggered a phantom rollback,
  and each rollback spawned another pair of orphan daemons. Updater now
  opens the encrypted registry via `openUpdaterRegistry`, falling back to
  the legacy file registry only on hard failure.
- **Updater spawnDaemon bypasses relocation**: previously exec'd
  `/usr/local/bin/appmon` directly, forcing the daemon to self-relocate via
  `syscall.Exec`. Now relocates before exec like
  `daemon.StartDaemonWithMode` does; child runs from a randomized path
  immediately.
- **Health-check timeout 10s → 30s**: absorbs SQLCipher key derivation +
  daemon self-relocate on cold-start. Too-tight timeout was the proximate
  cause of phantom rollbacks (combined with the registry split-brain).

### Architecture
- New `infra.Relocator` (relocator.go) — copies/hard-links binaries to
  obfuscated paths, sweeps stale entries, lists PIDs running from the
  relocator dir.
- New `infra.EnsureLaunchStub` (launch_stub.go) — stable randomized stub for
  the LaunchAgent plist, persisted as a secret in the encrypted registry,
  auto-refreshed when content drifts from the main binary.
- Watcher gains `sweepStaleRelocations` + `reapOrphans` hooks on its 60s
  binary-check tick.

## [0.4.0] - 2026-01-29

### Features
- **Freedom app protection**: Monitor and protect Freedom.app from being killed or disabled
  - Auto-restart Freedom.app within 5 seconds if killed
  - Auto-restore Login Items if removed
  - Reports helper status (can't fix, but warns user)
  - Graceful skip when Freedom.app not installed
- **Freedom health in status**: `appmon status` now shows Freedom protection status
  - Shows app running, proxy active, helper running, login item present
  - Warns when helper is missing (reinstall Freedom to fix)

### Architecture
- **Testable design**: Extracted `CommandRunner` and `FileChecker` interfaces for dependency injection
- **~98% test coverage** for Freedom protection module
- **Nil-safe logging**: Helper methods prevent panic when logger is nil

### Bug Fixes
- **PID conversion bug**: Fixed `string(rune(pid))` → `strconv.Itoa(pid)` for correct PID string conversion
- **Log spam**: Changed helper-missing log from WARN to Debug (runs every 5 seconds)

## [0.3.0] - 2026-01-28

### Features
- **Self-update command**: `appmon update` downloads and installs the latest version from GitHub
  - Automatic rollback on failure (daemon startup failure, health check failure)
  - Creates rollback backup before update, restores on any error
  - Step-by-step progress output during update process
- **Local binary testing**: `appmon update --local-binary ./path/to/binary` for testing updates without GitHub
- **Idempotent start command**: `appmon start` now handles version comparison
  - Upgrade: Running older binary auto-updates and restarts daemons
  - Same version: Prints "already running, up to date"
  - Downgrade prevention: Refuses to downgrade running newer version
- **Daemon version tracking**: `appmon status` now shows both CLI and daemon versions
  - Warning when CLI version differs from running daemon version
- **Mode switching cleanup**: Automatically removes stale plist from other mode when switching (user↔system)

### Improvements
- Idempotent plist operations: `NeedsUpdate()`, `Update()`, `CleanupOtherMode()` methods
- Proper SUDO_USER handling: `getRealUserHome()` correctly resolves user's home under sudo
- PID > 0 guards in `VerifyDaemonsHealthy()` to prevent signaling PID 0
- Error propagation in `generatePlistContent()` for better debugging
- Proper `/dev/null` file descriptor handling for daemon spawning
- Removed sensitive paths (binary path, plist path) from status output

### Documentation
- Added health status system requirements (`requirements/app_mon/3_health_status_system.md`)
- Added encrypted registry & server sync requirements (`requirements/app_mon/4_encrypted_registry_server_sync.md`)
- Added implementation artifacts for encrypted registry feature

## [0.2.0] - 2026-01-28

### Features
- **Sudo/non-sudo auto-detection**: Automatically detects execution mode based on effective UID
  - `sudo appmon start` → LaunchDaemon (system-wide, /usr/local/bin)
  - `appmon start` → LaunchAgent (user-space, ~/.local/bin)
- **GitHub fallback restoration**: When local backups are corrupted/missing, automatically downloads latest release from GitHub
- **Execution mode configuration**: Separate paths for binary, plist, and backups based on mode

### Bug Fixes
- **Atomic binary writes**: Use temp file → sync → chmod → rename pattern to prevent corruption during copy
- **Timeout conflict**: Separate API timeout (30s) from download timeout (5min) to prevent large download failures
- **Daemon executable path**: Use installed binary path instead of `os.Executable()` for daemon spawning
- **Daemon mode detection**: Fix subprocess always using user mode regardless of actual execution context

### Improvements
- Remove unused `BackupDir` from `ExecModeConfig` (misleading API)
- Update launchd comment to reflect actual behavior (`launchctl load` vs bootstrap)
- Add regression tests for all bug fixes

### Documentation
- Add Freedom app implementation analysis (`artifacts/documents/topics/freedom_implementation_detail.md`)
- Add future enhancements roadmap (`requirements/app_mon/3_future_enhancements.md`)
- Update non-functional requirements for CI verification and code review process

## [0.1.0] - 2026-01-26

### Features
- Initial release
- Process killing for Steam and Dota 2
- File/directory deletion for blocked apps
- Mutual daemon protection (watcher ↔ guardian)
- LaunchAgent auto-start on login
- Obfuscated process names
- Binary self-backup and restoration
