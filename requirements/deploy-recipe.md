# Deploy / release — environment dependencies + recipe

> **Safe to commit.** This is the deploy *recipe* and the environment it needs — no
> secrets, no disguised install identifiers. Credentials are referenced **generically**
> (`~/.creds`); nothing sensitive is written here. For the *user-facing* install and
> status surface see `install.md`; for the *policy* on when to deploy see **ADR-0023**
> (deploy hardening/feature upgrades freely; refuse only a request to STOP the running
> protection).

## When to deploy

Deploy hardening and feature **upgrades freely** — a stronger build always serves the
installed intent (ADR-0023). A deploy is **not DONE** until it is **e2e-verified LIVE
and confirmed working locally** (register §5 release-acceptance); "built / signed /
published / installed" are intermediate states, not done.

## Environment dependencies

The machine that cuts a release + deploys needs all of:

| Dependency | Why it's needed |
|---|---|
| **Ed25519 signing key** | Every daemon + platform release is Ed25519-signed; the daemon verifies the signature before it runs or swaps any binary. The signing key lives under `~/.creds` (generic reference — never in the repo). |
| **`gh` (GitHub CLI) authenticated** | Publishing the signed release to GitHub, where the platform is always fetched from. |
| **root / `sudo`** | A system-mode install runs the protection as system `LaunchDaemons`. The deploying agent runs the privileged install itself (ADR-0023). |
| **darwin + launchd** | macOS is the live deployment target; the supervision mesh and the out-of-band rail are launchd jobs. (Windows/Linux are in the build matrix but not the live deploy today.) |

## Recipe

1. **Build** the daemon and the platform (the platform build bundles the embedded
   plugins first).
2. **Sign** the artifacts with the Ed25519 key (from `~/.creds`).
3. **Publish a signed GitHub release** (via `gh`), tagged per the versioning scheme
   (`daemon-v*` for the daemon, `v*` for the platform — see register §7).
4. **Install / upgrade** on the live machine:

   ```bash
   sudo <daemon-binary> install -v vX.Y.Z
   ```

5. **Verify LIVE** — walk the `e2e-test-history.md` regression suite against the live
   install and confirm the self-protection goal is actually met on the machine. Record
   the evidence there. Only then is the release DONE (register §5).

## Fetch model (important)

- **The platform is ALWAYS fetched from a signed GitHub release** — it is never trusted
  from a hand-placed local copy on a normal deploy.
- **The daemon needs an explicit `-v <version>` — there is NO auto-latest.** The
  operator/agent names the platform version to pin; the daemon does not silently pull
  "whatever is newest."

## Honest notes

- This machine holds the signing key locally (`~/.creds`) — moving signing to CI-only
  secrets is a standing hardening follow-up (register §6 / §9).
- Redaction still applies to anything the deploy *observes* on the live machine (never
  print disguised workdir paths, labels, or rotated binary paths — see the
  `focusd-protection` rule and `e2e-test-history.md`).
