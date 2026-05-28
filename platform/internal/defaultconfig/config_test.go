package defaultconfig

import (
	"os"
	"path/filepath"
	"testing"
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

// An override replaces an existing job by ID (e.g. user wants
// dns-block-reconcile disabled), and does NOT remove other defaults.
func TestOverrideReplacesByIDPreservesOthers(t *testing.T) {
	defaultCfg, _, _ := LoadWithOverrides("")
	// Pick the first job ID from defaults to override.
	targetID := defaultCfg.Jobs[0].ID

	p := filepath.Join(t.TempDir(), "override.yaml")
	override := `platform:
  log_level: debug
jobs:
  - id: ` + targetID + `
    plugin: ` + defaultCfg.Jobs[0].Plugin + `
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
	cfg, _, err := LoadWithOverrides(p)
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
	var found *bool
	for i := range cfg.Jobs {
		if cfg.Jobs[i].ID == targetID {
			b := cfg.Jobs[i].Enabled
			found = &b
		}
	}
	if found == nil {
		t.Fatalf("overridden job %q vanished", targetID)
	}
	if *found {
		t.Fatalf("override should have disabled %q", targetID)
	}
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
