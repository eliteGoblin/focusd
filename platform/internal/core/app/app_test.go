package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
	"github.com/eliteGoblin/focusd/platform/internal/testutil"
)

const sampleConfig = `
platform:
  log_level: debug
jobs:
  - id: demo_job
    plugin: demo-job
    enabled: true
    schedule: "*/5 * * * *"
    timeout: "10s"
services:
  - id: browser_monitor
    plugin: browser-monitor
    enabled: true
`

func writeUserConfig(t *testing.T, fa *testutil.FakeAdapter, body string) {
	t.Helper()
	cfgPath, _ := fa.DefaultConfigPath(osadapter.ModeUser)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapUserModeFromAdapterDefaults(t *testing.T) {
	fa := testutil.NewFakeAdapter(t.TempDir())
	writeUserConfig(t, fa, sampleConfig)

	a, err := Bootstrap(Options{Adapter: fa})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	defer a.Close()

	if a.Mode != osadapter.ModeUser {
		t.Errorf("mode = %q, want user", a.Mode)
	}
	if len(a.Config.Jobs) != 1 || len(a.Config.Services) != 1 {
		t.Errorf("config not loaded: %+v", a.Config)
	}
	sv, err := a.State.SchemaVersion()
	if err != nil || sv == 0 {
		t.Errorf("state db not migrated: v%d err=%v", sv, err)
	}

	// state.db + log file must land under the user root, never system.
	stateDir, _ := fa.DefaultStateDir(osadapter.ModeUser)
	if _, err := os.Stat(filepath.Join(stateDir, "state.db")); err != nil {
		t.Errorf("state.db missing under user state dir: %v", err)
	}
	if _, err := os.Stat(fa.SystemBase); !os.IsNotExist(err) {
		t.Errorf("system root must not be touched in user mode (err=%v)", err)
	}
}

func TestBootstrapSystemModeViaDetection(t *testing.T) {
	// Realistic system-mode boot: the process is launched as root, so
	// DetectRunMode reports system and the system-root config is used.
	fa := testutil.NewFakeAdapter(t.TempDir())
	fa.Mode = osadapter.ModeSystem
	fa.IsSystem = true
	cfg := "platform:\n  run_mode: system\njobs: []\n"
	sysCfg, _ := fa.DefaultConfigPath(osadapter.ModeSystem)
	os.MkdirAll(filepath.Dir(sysCfg), 0o755)
	os.WriteFile(sysCfg, []byte(cfg), 0o644)

	a, err := Bootstrap(Options{Adapter: fa})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	defer a.Close()
	if a.Mode != osadapter.ModeSystem {
		t.Errorf("mode = %q, want system", a.Mode)
	}
	// User root must remain untouched — modes are fully isolated.
	if _, err := os.Stat(fa.UserBase); !os.IsNotExist(err) {
		t.Errorf("user root must not be touched in system mode (err=%v)", err)
	}
}

func TestBootstrapSystemModeWithoutPrivilegeFails(t *testing.T) {
	fa := testutil.NewFakeAdapter(t.TempDir())
	fa.IsSystem = false
	writeUserConfig(t, fa, sampleConfig)

	_, err := Bootstrap(Options{Adapter: fa, ForceMode: osadapter.ModeSystem})
	if err == nil || !strings.Contains(err.Error(), "system privilege") {
		t.Fatalf("expected system-privilege error, got %v", err)
	}
}

func TestBootstrapMissingConfigFails(t *testing.T) {
	fa := testutil.NewFakeAdapter(t.TempDir())
	if _, err := Bootstrap(Options{Adapter: fa}); err == nil {
		t.Fatal("expected error when config absent")
	}
}

func TestBootstrapFailsWhenLogDirUnresolvable(t *testing.T) {
	fa := testutil.NewFakeAdapter(t.TempDir())
	fa.FailLogDir = true
	writeUserConfig(t, fa, sampleConfig)
	if _, err := Bootstrap(Options{Adapter: fa}); err == nil {
		t.Fatal("expected error when log dir cannot be resolved")
	}
}

func TestBootstrapFailsWhenStateDirUnresolvable(t *testing.T) {
	fa := testutil.NewFakeAdapter(t.TempDir())
	fa.FailStateDir = true
	writeUserConfig(t, fa, sampleConfig)
	if _, err := Bootstrap(Options{Adapter: fa}); err == nil {
		t.Fatal("expected error when state dir cannot be resolved")
	}
}

func TestDiscoverPluginsSyncsInventory(t *testing.T) {
	fa := testutil.NewFakeAdapter(t.TempDir())
	writeUserConfig(t, fa, sampleConfig)
	a, err := Bootstrap(Options{Adapter: fa})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	defer a.Close()

	pdir, _ := fa.DefaultPluginDir(osadapter.ModeUser)
	ks := filepath.Join(pdir, "demo-job")
	os.MkdirAll(ks, 0o755)
	manifest := `{"id":"demo-job","name":"Demo Job","version":"1.0.0",
"type":"job","protocol_version":"1","entrypoint":"./demo-job",
"supported_os":["` + fa.OS + `"],"supported_arch":["` + fa.Arch + `"],
"required_privilege":"user","run_as":"current_user"}`
	os.WriteFile(filepath.Join(ks, "plugin.json"), []byte(manifest), 0o644)
	os.WriteFile(filepath.Join(ks, "demo-job"), []byte("#!/bin/sh\n"), 0o755)

	found, err := a.DiscoverPlugins()
	if err != nil {
		t.Fatalf("DiscoverPlugins: %v", err)
	}
	if len(found) != 1 || !found[0].OK {
		t.Fatalf("expected 1 OK plugin, got %+v", found)
	}
	rows, _ := a.State.Plugins.List()
	if len(rows) != 1 || rows[0].ID != "demo-job" {
		t.Errorf("inventory not synced: %+v", rows)
	}
}

func TestBootstrapFailsWhenPluginDirUnresolvable(t *testing.T) {
	fa := testutil.NewFakeAdapter(t.TempDir())
	fa.FailPluginDir = true
	writeUserConfig(t, fa, sampleConfig)
	if _, err := Bootstrap(Options{Adapter: fa}); err == nil {
		t.Fatal("expected error when plugin dir cannot be resolved")
	}
}

func TestPluginDirOverrideUsed(t *testing.T) {
	fa := testutil.NewFakeAdapter(t.TempDir())
	writeUserConfig(t, fa, sampleConfig)
	pdir := t.TempDir() // outside adapter layout
	a, err := Bootstrap(Options{Adapter: fa, PluginDir: pdir})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	defer a.Close()
	found, err := a.DiscoverPlugins()
	if err != nil {
		t.Fatalf("DiscoverPlugins: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("empty override dir should yield no plugins, got %d", len(found))
	}
}

func TestBuildSchedulerRegistersJobsForDiscoveredPlugins(t *testing.T) {
	fa := testutil.NewFakeAdapter(t.TempDir())
	writeUserConfig(t, fa, sampleConfig) // job demo-job + service browser_monitor
	a, err := Bootstrap(Options{Adapter: fa})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	defer a.Close()

	pdir, _ := fa.DefaultPluginDir(osadapter.ModeUser)
	ks := filepath.Join(pdir, "demo-job")
	os.MkdirAll(ks, 0o755)
	m := `{"id":"demo-job","name":"Demo Job","version":"1.0.0","type":"job",
"protocol_version":"1","entrypoint":"./demo-job","supported_os":["` + fa.OS +
		`"],"supported_arch":["` + fa.Arch + `"],"required_privilege":"user","run_as":"current_user"}`
	os.WriteFile(filepath.Join(ks, "plugin.json"), []byte(m), 0o644)
	os.WriteFile(filepath.Join(ks, "demo-job"), []byte("#!/bin/sh\necho '{\"status\":\"ok\"}'\n"), 0o755)

	s, n, err := a.BuildScheduler()
	if err != nil {
		t.Fatalf("BuildScheduler: %v", err)
	}
	if n != 1 || s == nil {
		t.Fatalf("registered = %d, want 1", n)
	}
}

func TestBootstrapExplicitPathsOverride(t *testing.T) {
	fa := testutil.NewFakeAdapter(t.TempDir())
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "custom.yaml")
	os.WriteFile(cfgPath, []byte("jobs: []\n"), 0o644)
	dbPath := filepath.Join(dir, "custom.db")

	a, err := Bootstrap(Options{Adapter: fa, ConfigPath: cfgPath, StateDBPath: dbPath})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	defer a.Close()
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("state db not created at explicit path: %v", err)
	}
}
