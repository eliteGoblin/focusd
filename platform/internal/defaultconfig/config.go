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
// the embedded default with the optional override file at overridePath,
// plus a list of warnings the caller should log (e.g. plugin-id swap
// detected; possible typo). Behaviour:
//
//   - overridePath == ""        → embedded default only, no warnings.
//   - overridePath does not exist → embedded default only (no error).
//   - overridePath exists       → parse + merge per Merge().
//
// Validation of both files happens via config.Parse so a malformed
// override is rejected at the boundary. A race-deleted override surfaces
// as a read error (not a silent fallback) — fail loud, by design.
func LoadWithOverrides(overridePath string) (*config.Config, []string, error) {
	base, err := config.Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("parse embedded default config: %w", err)
	}
	if overridePath == "" {
		return base, nil, nil
	}
	if _, err := os.Stat(overridePath); err != nil {
		if os.IsNotExist(err) {
			return base, nil, nil
		}
		return nil, nil, fmt.Errorf("stat override %s: %w", overridePath, err)
	}
	overrideRaw, err := os.ReadFile(overridePath)
	if err != nil {
		return nil, nil, fmt.Errorf("read override %s: %w", overridePath, err)
	}
	over, err := config.Parse(overrideRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("parse override %s: %w", overridePath, err)
	}
	cfg, warnings := Merge(base, over)
	return cfg, warnings, nil
}

// Merge returns base with per-field overlays from over applied + a list
// of human-readable warnings (e.g. a same-ID override pointing at a
// different plugin — possibly a typo on the user's part):
//
//   - Platform.LogLevel / Platform.RunMode: replaced if set in over.
//   - Jobs / Services: per-ID. An override entry replaces the
//     same-ID base entry; new IDs are appended. IDs only in base
//     stay as-is (so users never lose default jobs silently).
//
// TIGHTEN-ONLY — the "no inside door handle" rule (this is the LIVE home of
// that guarantee; the old unwired reconcile.Desired assertion was deleted):
// the override file is UNSIGNED, so it may CUSTOMISE or STRENGTHEN a baked job
// but it may NOT DISABLE a baked-enabled protection. A root user who finds the
// disposable workdir must not be able to switch a default protection off by
// dropping `enabled: false` into config.yaml. Such an override is refused —
// the job stays enabled and a warning is emitted. Additive/tightening changes
// (brand-new jobs, enabling a baked-disabled job, tuning an already-enabled
// job's config/schedule) still apply. (There is deliberately no "delete" — an
// accidental or malicious removal of the default protections is exactly what
// this whole design avoids.)
//
// NOTE on map aliasing: `Job.Config` and `Service.Config` are reference
// maps; base entries that aren't overridden share their map with the
// embedded default. The merged Config is treated as read-only after
// construction, so this is intentional (zero-copy of unchanged jobs).
func Merge(base, over *config.Config) (*config.Config, []string) {
	out := *base
	var warnings []string
	if over.Platform.LogLevel != "" {
		out.Platform.LogLevel = over.Platform.LogLevel
	}
	// TIGHTEN-ONLY: run_mode selects which plugin privilege class runs
	// (system vs user). An unsigned override that forces `user` on a
	// root-launched platform would make every run_as:system job (dns-block,
	// kill-steam, network-block) report "unavailable" and go silently
	// unscheduled — a TOTAL disable of the core enforcement layers via one
	// field, and exactly the "inside door handle" this design forbids. Refuse
	// an override of run_mode: it comes from the baked default or OS
	// auto-detect only (out already carries base.Platform.RunMode).
	if over.Platform.RunMode != "" && over.Platform.RunMode != base.Platform.RunMode {
		warnings = append(warnings, fmt.Sprintf(
			"override run_mode %q refused (tighten-only); run_mode is set by the baked default / OS auto-detect",
			over.Platform.RunMode))
	}
	out.Jobs, warnings = mergeJobs(base.Jobs, over.Jobs, warnings)
	out.Services, warnings = mergeServices(base.Services, over.Services, warnings)
	return &out, warnings
}

func mergeJobs(base, over []config.Job, warnings []string) ([]config.Job, []string) {
	idx := make(map[string]int, len(base))
	out := make([]config.Job, 0, len(base)+len(over))
	for _, j := range base {
		out = append(out, j)
		idx[j.ID] = len(out) - 1
	}
	for _, j := range over {
		if i, ok := idx[j.ID]; ok {
			// TIGHTEN-ONLY ("no inside door handle"): an UNSIGNED workdir
			// override must never DISABLE a baked-enabled protection. Refuse
			// the disable — keep the job enabled, warn — then apply the rest
			// of the override (schedule/config tuning is still allowed).
			if out[i].Enabled && !j.Enabled {
				warnings = append(warnings, fmt.Sprintf(
					"job %q override tried to disable a baked-enabled protection; refused (tighten-only)", j.ID))
				j.Enabled = true
			}
			// Architect-review #4: a same-ID override pointing at a
			// different plugin is almost always a user typo (e.g.
			// `plugin: kil-steam`) that would silently no-op the
			// default. Surface it as a warning; do not refuse — power
			// users may legitimately want to swap implementations.
			if j.Plugin != out[i].Plugin {
				warnings = append(warnings, fmt.Sprintf(
					"job %q overrides default plugin %q with %q (possible typo?)",
					j.ID, out[i].Plugin, j.Plugin))
			}
			out[i] = j
			continue
		}
		out = append(out, j)
		idx[j.ID] = len(out) - 1
	}
	return out, warnings
}

func mergeServices(base, over []config.Service, warnings []string) ([]config.Service, []string) {
	idx := make(map[string]int, len(base))
	out := make([]config.Service, 0, len(base)+len(over))
	for _, s := range base {
		out = append(out, s)
		idx[s.ID] = len(out) - 1
	}
	for _, s := range over {
		if i, ok := idx[s.ID]; ok {
			// TIGHTEN-ONLY (mirrors mergeJobs): an unsigned override must not
			// disable a baked-enabled service. Refuse the disable, warn, then
			// apply the rest of the override.
			if out[i].Enabled && !s.Enabled {
				warnings = append(warnings, fmt.Sprintf(
					"service %q override tried to disable a baked-enabled protection; refused (tighten-only)", s.ID))
				s.Enabled = true
			}
			if s.Plugin != out[i].Plugin {
				warnings = append(warnings, fmt.Sprintf(
					"service %q overrides default plugin %q with %q (possible typo?)",
					s.ID, out[i].Plugin, s.Plugin))
			}
			out[i] = s
			continue
		}
		out = append(out, s)
		idx[s.ID] = len(out) - 1
	}
	return out, warnings
}
