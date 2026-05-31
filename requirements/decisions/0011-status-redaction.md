# ADR-0011 — `daemon status` redaction is structural, not best-effort

- **Status:** accepted (2026-06-01)
- **Decided by:** Frank (product owner) + architect review + BA review
- **Feature:** [09-status-command](../features/09-status-command.md)

## Context

`daemon status` exists to answer "is focusd working?" without the operator
running discovery commands (`launchctl print`, `ls` the workdir, `pfctl -T
show`) — because those enumerate exactly the disguised identifiers the design
hides, and the threat model treats the operator's own weak-moment self as the
adversary. The whole value of the command evaporates if its *own* output leaks
those strings.

The naïve implementation is a renderer that probes everything, then "remembers"
to strip the secrets before printing. That is best-effort: one new field, one
new error path that prints the underlying value, one `fmt.Errorf("…%s…",
workdir)` that bubbles up — and the disguised path is on screen in front of the
person it was hidden from. With this threat model, "we'll be careful in the
renderer" is not a control. The leak must be impossible to write by accident,
not merely discouraged.

## Decision

**Redaction is a property of the type system, enforced by a contract test —
not a renderer responsibility.**

1. Disguised values (workdir, launchd labels, daemon binary filename, pf
   anchor) are carried in a dedicated `redact.Token` type whose raw value is
   **unexported**. The type has **no** method that yields the raw string:
   its `String()` returns `<redacted>` and its JSON marshalling emits an empty
   string. Code that holds a `Token` *cannot* print the secret — there is no
   accessor that returns it.

2. Probes return **primitives** (counts, ages, versions, verdicts) plus
   `Token`s. The renderer only ever prints primitives. A `Token` can appear in
   a snapshot struct, but only ever renders as `<redacted>`.

3. The contract is pinned by a **snapshot test**: feed the status engine
   deliberately poisonous inputs (a real-looking disguised workdir, label
   base, and pf anchor), render both text and json, and assert the bytes match
   **none** of the forbidden patterns (Application-Support workdir paths,
   `com.<...>.<hex>` label shapes, `*.plist`, the pf anchor literal). If any
   future change leaks a token, this test fails. The test *is* the contract.

## Alternatives considered

- **Scrub in the renderer (regex / blocklist over the final string).**
  Rejected: best-effort, fragile, and silently wrong the moment a token
  reaches output by a path the scrubber didn't anticipate. Defends the symptom,
  not the cause.
- **Don't carry the tokens at all — probe, derive booleans, discard.**
  Tempting, but the probes genuinely need the raw values to *do* their work
  (stat the workdir, `launchctl print` the labels, `pfctl -a <anchor>`). The
  values must exist in memory; the discipline must be that they can't be
  *printed*. `Token` encodes exactly that: usable internally, unprintable
  externally.
- **A `--show-paths` debug escape hatch.** Rejected outright: it reopens the
  precise leak the feature closes, for a benefit (operator sees the raw path)
  that the threat model says is a liability, not a feature.

## Consequences

- A whole class of leak — "someone printed the workdir in an error message" —
  becomes a **compile-shaped** mistake (you'd have to add an accessor that
  doesn't exist) rather than a review-catch.
- The snapshot test is now a standing guard: any future status field is
  checked against the forbidden-pattern set automatically.
- Slightly more ceremony at the probe boundary (wrap disguised values in
  `Token` instead of passing raw strings). Judged a clearly worthwhile cost for
  making the load-bearing requirement structural.
- Establishes a reusable pattern: any future command that must read internal
  state but show it safely can carry disguised values as `Token`s.

## References
- Feature spec: `../features/09-status-command.md`
- Threat model: `../REQUIREMENTS_REGISTER.md` §3, §5 (redaction rule)
</content>
