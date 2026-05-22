package config

import (
	"strings"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
)

const validYAML = `
platform:
  run_mode: user
  log_level: debug
jobs:
  - id: kill_steam_periodic
    plugin: kill-steam
    enabled: true
    schedule: "*/5 * * * *"
    timeout: "10s"
    retry: 1
    allow_overlap: false
    config:
      process_names: ["Steam"]
services:
  - id: browser_monitor
    plugin: browser-monitor
    enabled: true
    restart_policy: always
    health_check_interval: "30s"
    startup_timeout: "10s"
    config:
      watch_interval: "5s"
`

func TestParseValid(t *testing.T) {
	cfg, err := Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Platform.RunMode != osadapter.ModeUser {
		t.Errorf("run_mode = %q", cfg.Platform.RunMode)
	}
	if len(cfg.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(cfg.Jobs))
	}
	j := cfg.Jobs[0]
	if j.Timeout.Std() != 10*time.Second {
		t.Errorf("timeout = %v, want 10s", j.Timeout.Std())
	}
	if j.Config["process_names"] == nil {
		t.Error("opaque plugin config not preserved")
	}
	if len(cfg.Services) != 1 || cfg.Services[0].ID != "browser_monitor" {
		t.Error("service block not parsed")
	}
}

func TestDefaultsApplied(t *testing.T) {
	cfg, err := Parse([]byte("jobs: []\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Platform.LogLevel != "info" {
		t.Errorf("default log_level = %q, want info", cfg.Platform.LogLevel)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	_, err := Parse([]byte("platform:\n  nope: 1\n"))
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestInvalidDuration(t *testing.T) {
	y := `
jobs:
  - id: j
    plugin: p
    schedule: "* * * * *"
    timeout: "not-a-duration"
`
	if _, err := Parse([]byte(y)); err == nil || !strings.Contains(err.Error(), "duration") {
		t.Fatalf("expected duration error, got %v", err)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]string{
		"missing job id":     "jobs:\n  - plugin: p\n    schedule: \"* * * * *\"\n",
		"missing plugin":     "jobs:\n  - id: j\n    schedule: \"* * * * *\"\n",
		"missing schedule":   "jobs:\n  - id: j\n    plugin: p\n",
		"negative retry":     "jobs:\n  - id: j\n    plugin: p\n    schedule: \"* * * * *\"\n    retry: -1\n",
		"duplicate job id":   "jobs:\n  - id: j\n    plugin: p\n    schedule: \"* * * * *\"\n  - id: j\n    plugin: q\n    schedule: \"* * * * *\"\n",
		"bad run_mode":       "platform:\n  run_mode: root\n",
		"missing service id": "services:\n  - plugin: p\n",
	}
	for name, y := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(y)); err == nil {
				t.Errorf("%s: expected validation error", name)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("/no/such/focusd-config.yaml"); err == nil {
		t.Error("expected error for missing file")
	}
}
