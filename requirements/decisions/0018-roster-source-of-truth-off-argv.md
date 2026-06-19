# ADR-0018 — The masked roster file, not process argv, is the single source of truth for the mesh labels

- **Status:** accepted (2026-06-19) · shipped PR #60
- **Refines / partly reverses:** [ADR-0014](0014-independent-mesh-labels-xor-roster.md)
  — its "a survivor can recover the roster from a masked workdir file" choice is
  kept and *strengthened*; its implicit reliance on **carrying the full roster on
  the process command line** is reversed.
- **Feature:** [FEATURE 14](../features/14-mesh-argv-leak-minimization.md)
- **Decided by:** Frank (product owner), BA review

## Context

A live finding: `ps` shows every mesh process's full command line to root, and
that command line currently bakes in the disguised identifiers the rest of the
system hides — the workdir path, the GitHub channel (a focusd-identity
giveaway), the platform asset, the heal interval, a test-mode flag, and, worst,
**the full comma-joined list of all three mesh labels in clear text**.

ADR-0014 (FEATURE 10) added a masked on-disk roster file *specifically* so the
three labels could not be grepped as a cluster. But because the labels also rode
on the command line, a single `ps … | grep` returned all three — the exact
`launchctl bootout` keys — in one line. argv silently undid the very
decorrelation ADR-0014 was meant to deliver.

The root cause is a placement mistake: ADR-0014 introduced the masked file as a
**cold-start recovery** copy while still treating argv as a convenient carrier
for the roster. argv was the wrong place to carry it, because argv is
world-readable to the process owner and cannot be masked.

## Decision

**Make the masked on-disk roster file the single source of truth for the three
mesh labels, and remove the labels from every process command line.**

- The mesh coordinates and self-heals from the masked roster file (and from
  in-memory state, per ADR-0014) — never from argv. No process carries the label
  list as an argument.
- A mesh process's command line is reduced to **its role plus a mesh marker** and
  nothing more. The GitHub channel and the platform asset are **compiled in or
  derived** (consistent with ADR-0017's "derive, don't configure"), not passed as
  visible argument values. The workdir flag, asset, and interval come off argv.

This keeps ADR-0014's masked-file mechanism and self-heal-from-memory guarantee
intact — it simply moves the *authority* fully onto the masked file and stops
duplicating the secret in a place that cannot be hidden.

## Alternatives considered

- **Keep the roster on argv, just drop the channel/asset.** Rejected: the three
  labels are the most damaging leak (they are the teardown keys); leaving them on
  argv keeps the `ps` cluster-find fully intact and defeats FEATURE 10.
- **Try to mask/obfuscate the argv string.** Rejected as not-KISS and
  over-claiming: argv is plaintext to the owner by OS design; masking it is theatre,
  and the masked file already exists for exactly this purpose.
- **Per-role binary copies to also hide more in argv[0].** Out of scope here — a
  larger, less-KISS change; and it still cannot hide that *a* binary exists at the
  workdir path. The workdir path leak via argv[0] is recorded as an accepted
  limitation, not engineered around in this ADR.

## Consequences

- A `ps` listing no longer yields the three `launchctl bootout` keys, the focusd
  channel tell, or the workdir/asset/interval — the decorrelation ADR-0014
  intended now actually holds at runtime, not just on disk.
- One source of truth for the labels (the masked file), removing a duplicated
  secret and a class of "argv leaks what the file hides" bugs — correct by
  construction, in the spirit of ADR-0017.
- No change to protection behaviour: decorrelation, the ~2s heal cadence, and
  roster self-heal-from-memory are unchanged.

## Honest limitation

`argv[0]` — the binary's own path — is **always** visible in `ps` to the process
owner (root) on macOS and Linux; the OS offers no way to hide it. Because the
disguised binary lives inside the workdir, the **workdir/binary path remains
discoverable via `ps`** regardless of this decision. This ADR removes the
**labels** (the bootout keys) and the **focusd identity** from the command line;
it does **not** make the process invisible and must not be described as doing so.
The value delivered is precise and bounded: the three teardown keys and the
focusd-identity tell are no longer in `ps`. Separately, self-update's
path-rotation accumulates **stale background-task records** (Login Items "Allow in
the Background"); live protection does not depend on them, and cleaning them up is
iceboxed, not committed here.

## References
- Masked roster + decorrelation lineage: `0014-independent-mesh-labels-xor-roster.md`
- Reused mask pattern: FEATURE 3 (pubkey grep-resistance), register §4
- "Derive, don't configure" (channel/asset built-in, not passed): `0017-derive-dont-configure-recovery-inputs.md`
- Friction-not-cryptography principle: `../REQUIREMENTS_REGISTER.md` Section 5
