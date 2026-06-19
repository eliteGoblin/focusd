# Feature 14 — Stop the self-protection mesh from leaking its teardown keys in `ps`

- **Status:** ✅ shipped (2026-06-19, PR #60)
- **Decision:** [ADR-0018](../decisions/0018-roster-source-of-truth-off-argv.md)
- **Refines / partly reverses:** [FEATURE 10](10-mesh-label-decorrelation.md) /
  [ADR-0014](../decisions/0014-independent-mesh-labels-xor-roster.md) — F10 made
  the three mesh labels uncorrelatable on disk, but they still print in clear text
  on each process's command line, which undoes that work in `ps`.
- **Builds on:** [FEATURE 8](08-single-mesh.md) (the single mesh) ·
  [FEATURE 10](10-mesh-label-decorrelation.md) (the masked on-disk roster)

## What

A running mesh process exposes its full command line to anyone on the machine
(`ps` shows it to root). Today that command line bakes in the disguised
identifiers the rest of the system works hard to hide: the work-directory path,
the GitHub channel (a direct focusd-identity giveaway), the platform asset name,
the heal interval, a test-mode flag — and, worst of all, **the full list of all
three mesh roster labels in clear text**. Those three labels are precisely the
keys a person needs to tear the mesh down by hand.

This feature **minimizes what the mesh reveals in `ps`**. After it ships, a
running mesh process's visible command line shows only **its role and the fact
that it is a mesh member** — and nothing else. The three roster labels, the
GitHub channel, the workdir flag, the asset, and the interval are all removed
from the command line. The **masked on-disk roster file** (introduced in
FEATURE 10) becomes the single source of truth for the labels; the process no
longer carries them as arguments. The channel and asset become facts the binary
already knows or derives, not values passed in plain sight.

## Why

focusd is a commitment device whose adversary is the user's own weak-moment self.
FEATURE 10 went out of its way to make the three labels **uncorrelatable on
disk** — a masked roster file precisely so the labels could not be grepped as a
cluster. But the command line printed all three in clear text anyway, so a single
`ps` listing handed the weak-moment user **every teardown key in one line**. That
silently defeats F10's whole point.

The other leaked values each give the adversary a head start:

- **The three roster labels** are the exact keys for a manual teardown. This is
  the one that matters most.
- **The GitHub channel** is a plain focusd-identity tell — it announces *what
  this is*, turning a "mysterious background agent" into a known, searchable
  target.
- **The workdir flag, the asset name, and the heal interval** each narrow the
  search and confirm the install's shape.

Removing these from `ps` is squarely aligned with the product philosophy: it
**hardens** the commitment device by making impulsive teardown harder, and it
restores the decorrelation F10 was supposed to deliver.

## How it behaves (product rules)

- **Role + mesh marker, nothing more.** A running mesh process's command line, as
  seen in `ps`, reveals only which role it is and that it belongs to the mesh.
  Example shape only: a worker is `run --r <role> --mesh` and the ensurer is
  `ensure` — *not* a list of labels, a channel, or a workdir flag.
- **The labels live in the masked file, not on the command line.** The on-disk
  masked roster (FEATURE 10) is the single source of truth for the three labels.
  A process that needs the roster reads it from there; it does not receive the
  labels as visible arguments.
- **Identity facts are built in, not passed in.** The GitHub channel and the
  platform asset are compiled into the binary or derived from what it already
  knows — they are no longer visible argument values.
- **No behavior change to protection.** Decorrelation, self-heal cadence, and the
  roster's self-healing-from-memory guarantee (all from FEATURE 10) are
  unchanged. This feature only changes *what is visible in `ps`*, not how the mesh
  heals or coordinates.

## Acceptance criteria (testable behaviour)

1. A running mesh process's `ps` command line contains **none of the three
   roster labels**, **not the GitHub channel**, **not the workdir flag**, **not
   the platform asset name**, and **not the heal interval** — it contains only
   the process's **role** and a **mesh marker**.
2. With every mesh process running, a `ps … | grep`-style read of the full
   command lines **does not yield the set of `launchctl bootout` keys** — i.e.
   the three labels are not recoverable from `ps` output at all.
3. The masked on-disk roster file remains the **single source of truth** for the
   three labels: the mesh coordinates and self-heals using the file (and memory),
   with the labels absent from every process command line. FEATURE 10's behaviour
   (memory leads, file recovers, file self-heals, file is not plaintext) is
   preserved.
4. No regression in mesh protection: decorrelated labels, the ~2s heal cadence,
   and roster self-heal-from-memory all still hold (FEATURE 10 acceptance
   continues to pass).

## Verification (shipped PR #60 — what is and isn't proven)

Honest split between what was exercised against a real launchd mesh and what is
only proven by automated tests:

- **Live-verified (real launchd test-mesh):** a `ps`-style read of the running
  mesh's command lines carries **none** of the three roster labels, **not** the
  GitHub channel, **not** the asset, **not** the interval (acceptance #1/#2).
  Self-heal under bootout / kill / disable is at **parity-or-better with master**
  (acceptance #4).
- **Unit/integration-verified only (NOT exercised live):**
  - the **prod-truly-minimal** command line (role + mesh marker, no workdir flag)
    — the live test-mesh runs in test mode, which still carries a workdir, so the
    prod-minimal shape is proven by tests, not by the live run;
  - the **masked-file roster read** as the single source of truth — test mode
    computes the labels itself, so the live mesh did not read the masked file;
  - the **install-correlation migration matrix** (old / new / mixed plist
    generations) that lets a self-update find the existing install.
- **Not yet done (gated on the product owner):** a **live PROD mesh** and a
  **live self-update migration** — both require deploying FEATURE 14 to the system
  mesh, a separate step the product owner gates.

**Post-review hardening (Copilot-review fix, in PR #60):** both masked-roster
read sites now **reject any roster that isn't exactly the three expected labels**
(a truncated or edited roster file is treated as invalid and the process falls
back rather than coordinating on a corrupt label set). This closes a "partial
file looks valid" failure mode raised in review.

## Honest limitations

- **`argv[0]` is always visible — the workdir/binary path cannot be hidden.** The
  binary's own path (`argv[0]`) is shown by `ps` to the process owner (root) on
  macOS and Linux; the OS provides no way to hide it. Because the disguised
  binary lives **inside the workdir**, the **workdir/binary path stays
  discoverable via `ps`** no matter what this feature does. This feature does
  **not** and **cannot** make the mesh invisible. What it removes is the **three
  `launchctl bootout` keys** and the **focusd-identity tell** from the command
  line — not the process's existence or its binary path.
- **Friction, not a seal.** As with the rest of the local armor, this raises the
  cost of an *impulsive* teardown; it does not stop a determined, scripted
  attacker who reads the binary, the workdir, or the masked roster. The durable
  commitment weight remains the server-side override gate.
- **Stale Login-Items / background-task records are a separate, unsolved tell
  (known limitation).** Each self-update rotates the binary path (the anti-AMFI
  defense), which mints a fresh set of three launchd labels (one per role), and
  macOS's background-task store
  keeps the **old** records around as inert cruft — visible as stale duplicate
  entries in System Settings → Login Items → "Allow in the Background." This is a
  visual tell and clutter, but **live protection does not depend on the stale
  records** (their binaries are gone). Cleaning them up is captured as an icebox
  idea, not committed here — it may not be feasible if macOS offers no clean
  deregistration path (see Design questions and the icebox).

## Design questions / cross-cutting notes

- **What is the minimum a process legitimately needs on its command line?** The
  target is "role + mesh marker." Confirm in DESIGN that a relaunched survivor can
  reconstruct everything else (roster from the masked file, channel/asset built
  in) without any of the removed arguments.
- **3-platform Go still holds.** The argv-shaping and the built-in/derive of
  channel + asset should stay OS-agnostic where they can; the launchd-specific
  bits live behind the existing per-OS adapter seam. The daemon stays thin.
- **For the architect (stale background-task records):** is there a clean,
  supported way to deregister an old-generation label / remove its orphaned plist
  on self-update so the background-task store does not accumulate stale records?
  If yes, it could become its own small feature; if no, it stays an accepted
  limitation. Decide whether to pursue the icebox idea or document-and-accept.
