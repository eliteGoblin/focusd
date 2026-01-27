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

## Release Process

1. **Merge PR** - squash and merge to keep clean history
2. **Update CHANGELOG.md** - add release notes with categories:
   - Features (new functionality)
   - Bug Fixes (defect corrections)
   - Improvements (enhancements, refactoring)
   - Documentation (docs changes)
3. **Update version** - bump version in `cmd/appmon/main.go`
4. **Commit release prep** - commit CHANGELOG and version bump
5. **Tag release** - `git tag v{version}` (triggers CI release workflow)
6. **Push tag** - `git push origin v{version}`
7. **Verify release** - check GitHub releases page for artifacts
