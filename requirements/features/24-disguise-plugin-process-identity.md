# FEATURE 24 — Disguise all plugin & process identity (nothing greppable maps to the working folder)

- **Status:** 🟡 **BUILDING — VITAL invisibility bar OPEN** (hardening epic,
  2026-07-14; fix in progress on branch `stage1-consolidated`). The **production
  workdir still leaks** in the process table via an **environment variable that the
  env-dump form of `ps` (`ps -E`) prints**, so the sharpened VITAL bar is **NOT met**.
  The earlier **test-mode PASS (TC-28)** was on the **narrower** criteria (1–3) and
  **never exercised the env-dump vector** — **treat it as a latent failure until TC-30
  passes**. NOT live-verified, NOT deployed, NOT shipped. Came from a weakness the
  owner demonstrated **live**.
- **Extends:** [FEATURE 14](14-mesh-argv-leak-minimization.md) and
  [FEATURE 19](19-deeper-disguise.md) (which hid the **mesh** argv + role tell) —
  this carries the same discipline to the **plugin** processes.
- **Grade:** friction / hygiene (register §5) — disguise defeats casual
  `grep`/`ls`, not a determined reader of the source.

> ## ⚠️ VITAL invisibility bar (2026-07-14 — the developer is the adversary and keeps re-finding this)
>
> The **single most-used exploit** against this install is: `grep` the process list
> for a meaningful word → **read the working-directory path off the process** →
> `rm -rf` that folder → protection down. Removing that **grep → workdir → rm**
> chain is a **core, non-negotiable friction requirement**, not cosmetic. The
> working-directory path (and the binary identity + release version) must be
> invisible to a technical admin running `ps` and casual probes:
>
> - **Workdir in NEITHER the command line NOR the environment.** The path must not
>   appear in the process argv **or** its environment — so the plain, the
>   environment-dump (`ps -E` / `ps eww`), and the command-name (`ps -o comm`) forms
>   of `ps`, plus `find` / `lsof`, never print it. **Root cause of the current gap:**
>   the workdir was passed as a `--workdir` command-line argument; the half-fix only
>   moved it into an **environment variable**, which the environment-dump form of
>   `ps` still reveals.
> - **No common word maps to the install.** None of the target-app name, `platform`,
>   `daemon`, `block`, `dns`, `kill`, `focusd`, or the source-repo path / release tag
>   may appear in the process list or in on-disk names.
> - **Current build is PRE-FIX for this bar.** FEATURE 24's earlier test-mode PASS
>   (TC-28) checked the argv/name tells but did **not** exercise the environment-dump
>   (`ps -E`) vector, so the `--workdir`-in-env leak survived — a latent failure
>   behind a passing test (register §6). The VITAL bar is met **only** when a
>   production build carries the workdir in neither argv nor env and the exact
>   exploit below (**TC-30**) returns nothing.

## Why

Live the owner spotted the enforcement at a glance: a plain search of the process
list for the target app revealed the process-killing plugin, and a running
process's **name/version** (e.g. a version like `v0.16`) handed the owner the exact
string needed to **locate the working folder** and wipe it. Named/versioned plugin
processes are an at-a-glance tell that both **advertises** the install and **gives
the owner the key** to find and remove it — undercutting FEATURE 14/19's work to
strip the mesh command line.

> All identifiers here are **examples only** — not the real install's tokens.

## What

Reduce the plugin/process tells so a casual look does not reveal the install or
lead to the working folder.

- **Randomized/disguised process names.** All plugin processes run under
  **randomized, plausible names** — no target-app / "kill" / "platform" / version
  identifiers that a casual grep would catch.
- **Identity-free argv.** Plugin process command lines carry **zero identifying
  information**.
- **The platform process is disguised too.** Not just the plugins — the running
  protection engine also shows a generic, plausible name with no identifying argv.
- **On-disk plugin folders + logs use neutral names.** Nothing on disk under the
  working folder spells out a plugin's purpose.
- **Mesh labels don't cluster.** The supervisor entries look mutually dissimilar, so
  finding one doesn't reveal the others (carries FEATURE 10/19's discipline through).
- **Nothing greppable leads to the working folder.** No string visible in the
  running system maps back to the working-folder location.

## How it behaves (product rules)

- **Grep for the obvious enforcement words comes up empty.** Searching the live
  process list for the target-app name, "kill", "platform", or a version string
  returns **nothing** that identifies the install.
- **Argv carries nothing identifying.**
- **No running-process string leads to the working folder.**
- **No behaviour change** to what the plugins actually enforce.

## Acceptance criteria (strict, observable)

> **Verify ONLY in a sandbox / test-mode instance — never against the owner's real
> install.**

1. **No plugin tell in the process list.** A search of the live process list for the
   enforcement keywords (target-app name, "kill", "platform") **and** for a version
   string returns **nothing** that identifies the install.
2. **Argv carries nothing identifying.** Plugin process command lines carry **zero**
   identifying information.
3. **Nothing greppable maps to the working folder.** No string visible in the
   running system (process names / argv) leads to the working-folder location.
4. **No regression.** Enforcement behaviour is unchanged.
5. **Workdir invisible in the process table (VITAL — the literal contract).** The
   working-directory path appears in **NEITHER** the process command line **NOR**
   its environment. The exact exploit
   `sudo ps aux | grep -i support | grep -i application | grep '<release-tag>'` —
   **and** its `ps -E` / `ps eww` (environment dump), `ps -o comm`, `find`, and
   `lsof` variants — each return **nothing** that locates the workdir or identifies
   the binary. `--workdir` **never** appears as a command-line argument **or** an
   environment variable in a production build.
6. **No common word maps to the install (VITAL).** None of the target-app name,
   `platform`, `daemon`, `block`, `dns`, `kill`, `focusd`, or the source-repo path /
   release tag appears in the process list or in on-disk names.

> **Test-mode-tier status (2026-07-14, branch `hardening/hf4-disguise`):** the
> zero-leak greppability checks (criteria 1–3) **PASS** in the sandbox/test-mode
> instance — a sweep of the running system for the enforcement keywords, a version
> string, "focusd", "platform", plugin names, and the working-folder location comes
> up **empty**. Recorded as **TC-28 (test-mode PASS)**. **⚠️ But criteria 5–6 (the
> VITAL bar) are NOT yet met:** TC-28's sweep did **not** exercise the
> environment-dump (`ps -E`) vector, so the `--workdir`-in-env leak survived — the
> **current build is pre-fix** for the VITAL invisibility bar. Standing red-team +
> e2e gate: **TC-30 (OPEN)**. Still owed before ship: fix the env leak, then a
> **live** e2e pass on the real install + a signed deploy (hardening-epic
> consolidation, register §9).

## Honest limitations

- **`argv[0]` (the binary's own path) stays visible to root.** The OS gives no way
  to hide it — same honest limit as FEATURE 14/19. This removes the at-a-glance
  name/version tells; it does **not** make the process invisible.
- **Disguise never resists reading the source** (register §5). The durable defenses
  are time / real-world-state / behavioral, not obscurity. This is **casual-grade
  friction only** — it defeats `grep`/`ls`, not a determined reverse-engineer.
