# General Non-Functional Requirements

## Testing & Quality

- **Every bug fix must have a corresponding regression test** to prevent the bug from recurring.
- **Always verify CI status after each commit** - check format, lint, tests pass before considering PR ready.

## Code Review Process

- **When addressing Copilot/reviewer comments:**
  1. Verify each comment - determine if fix is needed
  2. If fix needed → implement fix, reply with "✅ Fixed" and brief description
  3. If deferred → add to `requirements/app_mon/3_future_enhancements.md`, reply with "📋 Deferred to future enhancement"
  4. Push fixes and verify CI passes before merge
