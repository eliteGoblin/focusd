// Package defaultconfig embeds the platform's default config.yaml and
// loads it as an OVERRIDE-MERGE: the embedded default is always the
// base; an optional `<workdir>/config.yaml` is a thin overlay merged on
// top per-job-id and per-platform-field. New jobs added by a new
// platform release auto-activate without any user action; user
// customisations to existing jobs are preserved across upgrades.
//
// (The old EnsureFile-once seed pattern was the bug behind v0.9.0's
// kill-steam plugin not activating on existing v0.8.0 installs: the
// stale on-disk seed beat the new embedded default. See PR notes.)
package defaultconfig

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/eliteGoblin/focusd/platform/internal/core/config"
)

//go:embed config.yaml
var raw []byte

// Bytes returns the embedded default config.yaml. Kept for tests and
// inspection callers.
func Bytes() []byte { return raw }

// LoadWithOverrides returns the active platform Config built by merging
// the embedded default with the optional override file at overridePath.
// Behaviour:
//
//   - overridePath == ""        → embedded default only.
//   - overridePath does not exist → embedded default only (no error).
//   - overridePath exists       → parse + merge per Merge().
//
// Validation of both files happens via config.Parse so a malformed
// override is rejected at the boundary.
func LoadWithOverrides(overridePath string) (*config.Config, error) {
	base, err := config.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse embedded default config: %w", err)
	}
	if overridePath == "" {
		return base, nil
	}
	if _, err := os.Stat(overridePath); err != nil {
		if os.IsNotExist(err) {
			return base, nil
		}
		return nil, fmt.Errorf("stat override %s: %w", overridePath, err)
	}
	overrideRaw, err := os.ReadFile(overridePath)
	if err != nil {
		return nil, fmt.Errorf("read override %s: %w", overridePath, err)
	}
	over, err := config.Parse(overrideRaw)
	if err != nil {
		return nil, fmt.Errorf("parse override %s: %w", overridePath, err)
	}
	return Merge(base, over), nil
}

// Merge returns base with per-field overlays from over applied:
//
//   - Platform.LogLevel / Platform.RunMode: replaced if set in over.
//   - Jobs / Services: per-ID. An override entry replaces the
//     same-ID base entry; new IDs are appended. IDs only in base
//     stay as-is (so users never lose default jobs silently).
//
// To disable a default job, put it in the override with `enabled:
// false`. To remove it entirely from the active set, override + disable
// (we deliberately do not provide a "delete" — accidental deletion of
// the default protections is exactly what this whole design avoids).
func Merge(base, over *config.Config) *config.Config {
	out := *base
	if over.Platform.LogLevel != "" {
		out.Platform.LogLevel = over.Platform.LogLevel
	}
	if over.Platform.RunMode != "" {
		out.Platform.RunMode = over.Platform.RunMode
	}
	out.Jobs = mergeJobs(base.Jobs, over.Jobs)
	out.Services = mergeServices(base.Services, over.Services)
	return &out
}

func mergeJobs(base, over []config.Job) []config.Job {
	idx := make(map[string]int, len(base))
	out := make([]config.Job, 0, len(base)+len(over))
	for _, j := range base {
		out = append(out, j)
		idx[j.ID] = len(out) - 1
	}
	for _, j := range over {
		if i, ok := idx[j.ID]; ok {
			out[i] = j
			continue
		}
		out = append(out, j)
		idx[j.ID] = len(out) - 1
	}
	return out
}

func mergeServices(base, over []config.Service) []config.Service {
	idx := make(map[string]int, len(base))
	out := make([]config.Service, 0, len(base)+len(over))
	for _, s := range base {
		out = append(out, s)
		idx[s.ID] = len(out) - 1
	}
	for _, s := range over {
		if i, ok := idx[s.ID]; ok {
			out[i] = s
			continue
		}
		out = append(out, s)
		idx[s.ID] = len(out) - 1
	}
	return out
}
