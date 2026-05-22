// Package config loads and validates the platform's desired-state YAML.
//
// YAML holds desired configuration only (human-editable). Observed state
// lives in SQLite, never here. The schema is validated fail-fast on load:
// a malformed config must stop the platform rather than run partially.
package config

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that (un)marshals as a Go duration string
// ("10s", "5m"), matching the spec's job config examples.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("duration must be a string like \"10s\": %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }
func (d Duration) Std() time.Duration        { return time.Duration(d) }

// Config is the full desired-state document.
type Config struct {
	Platform Platform  `yaml:"platform"`
	Jobs     []Job     `yaml:"jobs"`
	Services []Service `yaml:"services"` // typed-only; future service plugins
}

// Platform holds platform-wide settings.
type Platform struct {
	// RunMode optionally pins the run mode. Empty means auto-detect from
	// the running process via the OS adapter.
	RunMode  osadapter.RunMode `yaml:"run_mode"`
	LogLevel string            `yaml:"log_level"`
}

// Job is a scheduled invocation of a job plugin.
type Job struct {
	ID           string         `yaml:"id"`
	Plugin       string         `yaml:"plugin"`
	Enabled      bool           `yaml:"enabled"`
	Schedule     string         `yaml:"schedule"` // cron expression
	Timeout      Duration       `yaml:"timeout"`
	Retry        int            `yaml:"retry"`
	AllowOverlap bool           `yaml:"allow_overlap"`
	Config       map[string]any `yaml:"config"` // opaque, passed to plugin
}

// Service represents a future long-running service plugin. Parsed and
// validated for forward compatibility but not executed yet.
type Service struct {
	ID                  string         `yaml:"id"`
	Plugin              string         `yaml:"plugin"`
	Enabled             bool           `yaml:"enabled"`
	RestartPolicy       string         `yaml:"restart_policy"`
	HealthCheckInterval Duration       `yaml:"health_check_interval"`
	StartupTimeout      Duration       `yaml:"startup_timeout"`
	Config              map[string]any `yaml:"config"`
}

// Load reads, parses, and validates a config file. Unknown fields are
// rejected so typos fail fast rather than silently no-op.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return Parse(raw)
}

// Parse validates raw YAML bytes (separated from Load for testability).
func Parse(raw []byte) (*Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Platform.LogLevel == "" {
		c.Platform.LogLevel = "info"
	}
}

// Validate enforces structural invariants. Returns the first violation.
func (c *Config) Validate() error {
	if c.Platform.RunMode != "" && !c.Platform.RunMode.Valid() {
		return fmt.Errorf("platform.run_mode %q is invalid (use user|system or omit)", c.Platform.RunMode)
	}

	seenJob := make(map[string]struct{})
	for i, j := range c.Jobs {
		switch {
		case j.ID == "":
			return fmt.Errorf("jobs[%d]: id is required", i)
		case j.Plugin == "":
			return fmt.Errorf("job %q: plugin is required", j.ID)
		case j.Schedule == "":
			return fmt.Errorf("job %q: schedule is required", j.ID)
		case j.Retry < 0:
			return fmt.Errorf("job %q: retry must be >= 0", j.ID)
		case j.Timeout < 0:
			return fmt.Errorf("job %q: timeout must be >= 0", j.ID)
		}
		if _, dup := seenJob[j.ID]; dup {
			return fmt.Errorf("duplicate job id %q", j.ID)
		}
		seenJob[j.ID] = struct{}{}
	}

	seenSvc := make(map[string]struct{})
	for i, s := range c.Services {
		if s.ID == "" {
			return fmt.Errorf("services[%d]: id is required", i)
		}
		if s.Plugin == "" {
			return fmt.Errorf("service %q: plugin is required", s.ID)
		}
		if _, dup := seenSvc[s.ID]; dup {
			return fmt.Errorf("duplicate service id %q", s.ID)
		}
		seenSvc[s.ID] = struct{}{}
	}
	return nil
}
