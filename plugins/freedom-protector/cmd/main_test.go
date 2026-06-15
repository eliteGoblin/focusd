package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/plugins/freedom-protector/internal/reconciler"
)

// writeF writes a test fixture file, failing fast on I/O error so a real
// failure is diagnosable (not masked behind a later assertion).
func writeF(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadOptionsDefaultsWhenNoPath(t *testing.T) {
	opts, err := loadOptions("")
	if err != nil || opts != (reconciler.Options{}) {
		t.Errorf("empty path => zero opts,nil err; got %+v,%v", opts, err)
	}
}

func TestLoadOptionsFromConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "job.json")
	writeF(t, p, `{"job_id":"j","plugin_id":"freedom-protector","config":{`+
		`"app_path":"/A/Free.app","proxy_port":"9","proxy_rpcport":"10"}}`)
	opts, err := loadOptions(p)
	if err != nil {
		t.Fatalf("loadOptions: %v", err)
	}
	if opts.AppPath != "/A/Free.app" || opts.ProxyPort != "9" || opts.ProxyRPCPort != "10" {
		t.Errorf("got %+v", opts)
	}
}

func TestLoadOptionsIgnoresWrongTypesAndMissingKeys(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "job.json")
	// Non-string values and missing keys fall back to defaults (no error):
	// the knobs are optional overrides, not required input.
	writeF(t, p, `{"config":{"app_path":123,"proxy_port":true}}`)
	opts, err := loadOptions(p)
	if err != nil {
		t.Fatalf("loadOptions: %v", err)
	}
	if opts != (reconciler.Options{}) {
		t.Errorf("wrong-typed values should be ignored, got %+v", opts)
	}
}

func TestLoadOptionsErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	writeF(t, bad, `{not json`)
	if _, err := loadOptions(bad); err == nil {
		t.Error("expected parse error")
	}
	if _, err := loadOptions(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("expected read error")
	}
}

func TestRunUsageAndVersion(t *testing.T) {
	if code := run([]string{"version"}); code != 0 {
		t.Errorf("version exit = %d", code)
	}
	if code := run([]string{}); code != 2 {
		t.Errorf("no args exit = %d, want 2", code)
	}
	if code := run([]string{"bogus"}); code != 2 {
		t.Errorf("bad subcommand exit = %d, want 2", code)
	}
}

// Real run pointed at a non-existent app path => benign skip, exit 0.
// Nothing real is launched (Freedom absent at the override path).
func TestRunBenignSkipWhenFreedomAbsent(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "job.json")
	absent := filepath.Join(dir, "NoSuch.app")
	writeF(t, cfg, `{"job_id":"j","plugin_id":"freedom-protector","config":{`+
		`"app_path":"`+absent+`"}}`)
	if code := run([]string{"run", "--config", cfg}); code != 0 {
		t.Errorf("benign-skip exit = %d, want 0", code)
	}
}

func TestRunErrorOnBadConfig(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	writeF(t, bad, `{nope`)
	if code := run([]string{"run", "--config", bad}); code != 2 {
		t.Errorf("bad config exit = %d, want 2", code)
	}
}
