//go:build !e2e

package main

import "testing"

// TestWorkdirOverrideOffAlwaysEmpty is the release-build guard: workdirOverride
// MUST ignore both an explicit --workdir flag value AND (implicitly) the
// WorkdirEnvKey env var, returning "" so the platform always self-derives its
// workdir from its own binary location. This is what keeps the disguised prod
// child's workdir off BOTH argv and the environment. A regression here (honoring
// the flag/env in a release build) would reopen the `ps -E` workdir leak.
func TestWorkdirOverrideOffAlwaysEmpty(t *testing.T) {
	for _, in := range []string{"", "/tmp/wd", "/Users/x/Library/Application Support/.hidden/pw", "anything"} {
		if got := workdirOverride(in); got != "" {
			t.Errorf("workdirOverride(%q) = %q in a release (!e2e) build; want \"\" (must self-derive)", in, got)
		}
	}
}
