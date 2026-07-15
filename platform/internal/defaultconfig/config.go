// Package defaultconfig embeds the platform's default config.yaml and is
// the SINGLE source of enforced policy on the daemon-managed run path.
//
// There is no on-disk override: a config.yaml dropped into the workdir is
// INERT — never read — so a weak-moment edit (removing a job, flipping
// enabled:false) cannot loosen enforcement. Policy = the signed embedded
// default baked into the signed platform binary, and nothing else.
//
// (Rationale: the previous override-merge loader let an unsigned
// <workdir>/config.yaml disable default jobs. That was a tamper surface,
// so it was removed. Dev inspection of an arbitrary config file now goes
// through `platform validate --config <path>` (config.Load), never the run
// path. config→server is the future direction for policy delivery;
// embedding the signed default is the KISS interim.)
package defaultconfig

import (
	_ "embed"
	"fmt"

	"github.com/eliteGoblin/focusd/platform/internal/core/config"
)

//go:embed config.yaml
var raw []byte

// Bytes returns the embedded default config.yaml. Kept for tests and
// inspection callers.
func Bytes() []byte { return raw }

// Load parses and returns the embedded default Config. This is the only
// policy source on the daemon-managed run path — there is no override to
// merge. A malformed embedded default is a build defect, so a parse
// failure is a hard error (fail-fast).
func Load() (*config.Config, error) {
	cfg, err := config.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse embedded default config: %w", err)
	}
	return cfg, nil
}
