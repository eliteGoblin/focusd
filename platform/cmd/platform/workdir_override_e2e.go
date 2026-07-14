//go:build e2e

package main

import (
	"os"

	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
)

// workdirOverride (e2e build) honors a caller-supplied workdir: an explicit
// --workdir flag first, then the WorkdirEnvKey environment variable. This keeps
// the deterministic, self-contained e2e/test-mode lifecycle working (the daemon
// hands the sandbox workdir to the platform, and the legacy on-disk layout is
// bin/<v>/platform, which DeriveWorkdir's 2-levels-up rule would NOT resolve).
//
// A RELEASE binary is built WITHOUT this tag (see workdir_override_off.go): it
// ignores both the flag and the env entirely and always self-derives, so a
// disguised prod install can carry the workdir on NEITHER argv nor env.
func workdirOverride(flag string) string {
	if flag != "" {
		return flag
	}
	return os.Getenv(osadapter.WorkdirEnvKey)
}
