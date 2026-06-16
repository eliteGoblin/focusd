# Installing focusd (system mode) — one command

focusd installs as a **single command, in system mode**, which sets up *every*
layer at once. System mode is the recommended (and strongest) install: the
protection runs as system `LaunchDaemons`, so it does **not** appear in your
user "Login Items → Allow in the Background" list and cannot be toggled off
without admin rights.

## Install — one command

```bash
sudo ./<daemon-binary> install -v vX.Y.Z
```

- `sudo` → installs in **system mode** (runs as root / `LaunchDaemons`).
- `-v vX.Y.Z` → the platform (enforcement engine) version to pin.

That single command sets up **all** components:

| Layer | What it is |
|------|------------|
| **In-band mesh** | 3 mutually-respawning launchd entries (2 always-on workers + 1 periodic ensurer) that keep the platform alive and heal each other within ~2s. |
| **Out-of-band watchdog** | A separate rail that survives a *total* wipe of the mesh and re-installs it. Installed automatically by the same command — nothing extra to run. |
| **Platform + plugins** | The pinned platform engine, which runs every enforcement plugin on schedule: Steam/Dota2 process removal, DNS/host blocking, packet (pf) blocking, Claude-skill re-injection, and the Freedom keep-alive. |

There is no second step. `install` lays down the mesh, the watchdog, relocates
itself to a hidden path, and the workers bring the platform up on their first
reconcile.

## Check status — every component

```bash
sudo ./<daemon-binary> status
```

Shows a full health picture in one view:

```
focusd daemon status
  mode                   system
  protection engine      3/3 roles running        ← in-band mesh
  platform process       running
  platform version       desired=vX.Y.Z good=vX.Y.Z
  out-of-band watchdog   present                  ← out-of-band rail
platform protections
  kill-steam …           ok · …                   ← each plugin
  skill-guard …          ok · …
  freedom-protector …    ok · …
OVERALL                  HEALTHY
```

`status` is **redaction-safe** — it reports each component's *health*, never
the disguised paths, labels, or the watchdog's underlying mechanism.

## Notes

- **No stop command** — intentional friction. Disabling is not a CLI flag.
- **Uninstall is gated** — `uninstall` runs a multi-step, multi-hour commitment
  ritual before it tears anything down (and it removes the watchdog rail too).
- **Honest limit** — every local layer is *friction*, not an absolute wall: a
  determined admin can still tear it all down. The durable lock is the
  server-side override gate, not anything on this machine.
