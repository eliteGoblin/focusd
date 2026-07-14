package defaultconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/platform/internal/core/config"
	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
)

// The embedded default must always parse — no override required.
func TestLoadDefaultOnlyParses(t *testing.T) {
	cfg, _, err := LoadWithOverrides("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Jobs) == 0 {
		t.Fatal("embedded default has no jobs — that's the wrong baseline")
	}
}

// A missing override file is silently ignored (no error) — useful for
// fresh installs that have never written one.
func TestLoadMissingOverrideIsDefaultOnly(t *testing.T) {
	cfg, _, err := LoadWithOverrides(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	defaultCfg, _, _ := LoadWithOverrides("")
	if len(cfg.Jobs) != len(defaultCfg.Jobs) {
		t.Fatalf("missing override should equal default-only; got %d vs %d",
			len(cfg.Jobs), len(defaultCfg.Jobs))
	}
}

// FIX 2 (tighten-only, integration): an override that tries to DISABLE a
// baked-enabled protection is REFUSED — the job stays enabled and a warning is
// surfaced — while its non-disable fields (schedule) still merge and other
// defaults are preserved (replace, not append). This is the "no inside door
// handle": a root user who finds the workdir cannot switch a default
// protection off via unsigned config.yaml.
func TestOverrideCannotDisableBakedEnabledJob(t *testing.T) {
	defaultCfg, _, _ := LoadWithOverrides("")
	target := defaultCfg.Jobs[0] // dns-block-reconcile — baked enabled:true
	if !target.Enabled {
		t.Fatalf("test precondition: %q must be baked enabled", target.ID)
	}

	p := filepath.Join(t.TempDir(), "override.yaml")
	override := `platform:
  log_level: debug
jobs:
  - id: ` + target.ID + `
    plugin: ` + target.Plugin + `
    enabled: false
    schedule: "@every 1h"
    timeout: 1s
    retry: 0
    allow_overlap: false
    config: {}
`
	if err := os.WriteFile(p, []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, warnings, err := LoadWithOverrides(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Platform.LogLevel != "debug" {
		t.Fatalf("Platform.LogLevel not overridden: %q", cfg.Platform.LogLevel)
	}
	if len(cfg.Jobs) != len(defaultCfg.Jobs) {
		t.Fatalf("job count must be unchanged (replace, not append): %d vs %d",
			len(cfg.Jobs), len(defaultCfg.Jobs))
	}
	var got *config.Job
	for i := range cfg.Jobs {
		if cfg.Jobs[i].ID == target.ID {
			got = &cfg.Jobs[i]
		}
	}
	if got == nil {
		t.Fatalf("overridden job %q vanished", target.ID)
	}
	// The disable is REFUSED — the protection remains enabled...
	if !got.Enabled {
		t.Fatalf("override must NOT be able to disable baked-enabled %q (inside door handle)", target.ID)
	}
	// ...and the refusal is surfaced as a warning.
	if !hasWarning(warnings, "refused (tighten-only)") {
		t.Fatalf("a refused disable must warn, got warnings=%v", warnings)
	}
	// Non-disable fields still merge (customisation of an enabled job is allowed).
	if got.Schedule != "@every 1h" {
		t.Fatalf("non-disable override fields must still apply, schedule=%q", got.Schedule)
	}
}

// FIX 2: TIGHTENING is allowed — an override may ENABLE a baked-disabled job.
// Tighten-only refuses loosening (disable), not strengthening.
func TestOverrideCanEnableBakedDisabledJob(t *testing.T) {
	defaultCfg, _, _ := LoadWithOverrides("")
	var disabled *config.Job
	for i := range defaultCfg.Jobs {
		if !defaultCfg.Jobs[i].Enabled {
			disabled = &defaultCfg.Jobs[i]
			break
		}
	}
	if disabled == nil {
		t.Skip("no baked-disabled job to enable")
	}

	p := filepath.Join(t.TempDir(), "override.yaml")
	override := `jobs:
  - id: ` + disabled.ID + `
    plugin: ` + disabled.Plugin + `
    enabled: true
    schedule: "` + disabled.Schedule + `"
    timeout: 10s
    retry: 0
    allow_overlap: false
    config: {}
`
	if err := os.WriteFile(p, []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadWithOverrides(p)
	if err != nil {
		t.Fatal(err)
	}
	for i := range cfg.Jobs {
		if cfg.Jobs[i].ID == disabled.ID && !cfg.Jobs[i].Enabled {
			t.Fatalf("override must be able to ENABLE baked-disabled %q (tightening)", disabled.ID)
		}
	}
}

func hasWarning(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// New IDs in the override are appended (lets a user add custom jobs
// alongside the bundled defaults).
func TestOverrideNewIDIsAppended(t *testing.T) {
	defaultCfg, _, _ := LoadWithOverrides("")
	p := filepath.Join(t.TempDir(), "override.yaml")
	override := `jobs:
  - id: my-custom-job
    plugin: kill-steam
    enabled: false
    schedule: "@every 30m"
    timeout: 5s
    retry: 0
    allow_overlap: false
    config: {}
`
	if err := os.WriteFile(p, []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadWithOverrides(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Jobs) != len(defaultCfg.Jobs)+1 {
		t.Fatalf("new id must be appended; got %d vs %d+1",
			len(cfg.Jobs), len(defaultCfg.Jobs))
	}
	if cfg.Jobs[len(cfg.Jobs)-1].ID != "my-custom-job" {
		t.Fatalf("appended job should be the new one, got %q",
			cfg.Jobs[len(cfg.Jobs)-1].ID)
	}
}

// A malformed override is rejected loudly — no silent fallthrough to
// defaults that could mask configuration mistakes.
func TestOverrideMalformedIsError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "override.yaml")
	os.WriteFile(p, []byte("this is not: valid: yaml: at: all:\n  - {"), 0o644)
	if _, _, err := LoadWithOverrides(p); err == nil {
		t.Fatal("malformed override must surface as an error")
	}
}

// FIX 2 (unit, Merge directly): the required test that Merge REFUSES disabling
// a baked-enabled job. A same-ID override with enabled:false leaves the job
// enabled and warns; an override that enables a baked-disabled job is applied.
func TestMergeIsTightenOnlyForEnabled(t *testing.T) {
	base := &config.Config{Jobs: []config.Job{
		{ID: "guard", Plugin: "kill-steam", Enabled: true, Schedule: "@every 10s"},
		{ID: "idle", Plugin: "network-block", Enabled: false, Schedule: "@every 10s"},
	}}
	over := &config.Config{Jobs: []config.Job{
		{ID: "guard", Plugin: "kill-steam", Enabled: false, Schedule: "@every 10s"},  // try to DISABLE
		{ID: "idle", Plugin: "network-block", Enabled: true, Schedule: "@every 10s"}, // ENABLE (tighten)
	}}

	merged, warnings := Merge(base, over)
	byID := map[string]config.Job{}
	for _, j := range merged.Jobs {
		byID[j.ID] = j
	}
	if !byID["guard"].Enabled {
		t.Fatal("Merge must refuse disabling baked-enabled job 'guard' (inside door handle)")
	}
	if !byID["idle"].Enabled {
		t.Fatal("Merge must allow enabling baked-disabled job 'idle' (tightening)")
	}
	if !hasWarning(warnings, "refused (tighten-only)") {
		t.Fatalf("a refused disable must warn, got %v", warnings)
	}
}

// FIX 2 (run_mode tighten-only): an unsigned override cannot force run_mode.
// A `user` override on a root-launched platform would make every run_as:system
// protection report unavailable and silently unschedule — a total disable via
// one field. The override is refused (baked/auto-detect value kept) and warned.
func TestMergeRefusesRunModeOverride(t *testing.T) {
	base := &config.Config{} // baked run_mode empty ⇒ OS auto-detect
	over := &config.Config{Platform: config.Platform{RunMode: osadapter.ModeUser}}

	merged, warnings := Merge(base, over)
	if merged.Platform.RunMode != "" {
		t.Fatalf("override run_mode must be refused (kept baked/auto-detect), got %q", merged.Platform.RunMode)
	}
	if !hasWarning(warnings, "run_mode") {
		t.Fatalf("a refused run_mode override must warn, got %v", warnings)
	}
}

// A service disable is refused symmetrically (mergeServices tighten-only).
func TestMergeServicesTightenOnly(t *testing.T) {
	base := &config.Config{Services: []config.Service{
		{ID: "svc", Plugin: "guard", Enabled: true},
	}}
	over := &config.Config{Services: []config.Service{
		{ID: "svc", Plugin: "guard", Enabled: false},
	}}
	merged, warnings := Merge(base, over)
	if !merged.Services[0].Enabled {
		t.Fatal("Merge must refuse disabling a baked-enabled service")
	}
	if !hasWarning(warnings, "refused (tighten-only)") {
		t.Fatalf("refused service disable must warn, got %v", warnings)
	}
}
