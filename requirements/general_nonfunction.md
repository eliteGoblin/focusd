# General Non-Functional Requirements

## Testing Strategy

### Test Pyramid
- **Unit tests** (most): Test business logic in isolation (usecase, domain)
- **Integration tests** (some): Test component interactions (backup + GitHub, registry + filesystem)
- **E2E tests** (few): Manual verification of full flows (update command, daemon restart)

### What to Test
- **Core logic**: Enforcer, Updater, version comparison - high coverage
- **Infrastructure**: Real filesystem/process calls - test key paths, not every branch
- **CLI**: Manual E2E verification, not unit tests

### Testing Guidelines
- **TDD for complex logic** - write tests first for non-trivial business rules
- **Design for testability** - dependency injection in inner layers (usecase), real deps in outer (infra)
- **Don't over-mock** - infra code can use real filesystem/process calls in tests
- **Regression tests for bugs** - every bug fix needs a test to prevent recurrence
- **Coverage as guide, not goal** - aim for meaningful tests, not 100% coverage

## Code Review Process

- **When addressing Copilot/reviewer comments:**
  1. Verify each comment - determine if fix is needed
  2. If fix needed → implement fix, reply with "✅ Fixed" and brief description
  3. If deferred → add to `requirements/app_mon/future_enhancements.md`, reply with "📋 Deferred to future enhancement"
  4. Push fixes and verify CI passes before merge

## Release Process

1. **Merge PR** - squash and merge to keep clean history
2. **Update CHANGELOG.md** - add release notes with categories:
   - Features (new functionality)
   - Bug Fixes (defect corrections)
   - Improvements (enhancements, refactoring)
   - Documentation (docs changes)
3. **Commit release prep** - commit CHANGELOG update
4. **Tag release** - `git tag v{version}` (triggers CI release workflow, version set via ldflags)
5. **Push tag** - `git push origin v{version}`
6. **Verify release** - check GitHub releases page for artifacts
