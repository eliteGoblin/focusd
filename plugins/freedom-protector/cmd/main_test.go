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

// withStdin swaps os.Stdin for a pipe carrying data for the duration of
// fn, then restores it. Exercises the disguised (stdin) config path.
func withStdin(t *testing.T, data string, fn func()) {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = w.Write([]byte(data)); _ = w.Close() }()
	os.Stdin = r
	defer func() { os.Stdin = old; _ = r.Close() }()
	fn()
}

func TestLoadOptionsDefaultsWhenEmpty(t *testing.T) {
	opts, err := loadOptions(nil)
	if err != nil || opts != (reconciler.Options{}) {
		t.Errorf("nil raw => zero opts,nil err; got %+v,%v", opts, err)
	}
}

func TestLoadOptionsFromConfig(t *testing.T) {
	raw := []byte(`{"job_id":"j","plugin_id":"freedom-protector","config":{` +
		`"app_path":"/A/Free.app","proxy_port":"9","proxy_rpcport":"10"}}`)
	opts, err := loadOptions(raw)
	if err != nil {
		t.Fatalf("loadOptions: %v", err)
	}
	if opts.AppPath != "/A/Free.app" || opts.ProxyPort != "9" || opts.ProxyRPCPort != "10" {
		t.Errorf("got %+v", opts)
	}
}

func TestLoadOptionsIgnoresWrongTypesAndMissingKeys(t *testing.T) {
	// Non-string values and missing keys fall back to defaults (no error):
	// the knobs are optional overrides, not required input.
	opts, err := loadOptions([]byte(`{"config":{"app_path":123,"proxy_port":true}}`))
	if err != nil {
		t.Fatalf("loadOptions: %v", err)
	}
	if opts != (reconciler.Options{}) {
		t.Errorf("wrong-typed values should be ignored, got %+v", opts)
	}
}

func TestLoadOptionsErrors(t *testing.T) {
	if _, err := loadOptions([]byte(`{not json`)); err == nil {
		t.Error("expected parse error")
	}
}

// --- readJobConfig: --config (compat) vs stdin (disguised) ---

func TestReadJobConfig_StdinMatchesFile(t *testing.T) {
	body := `{"config":{"app_path":"/A/Free.app","proxy_port":"7"}}`

	dir := t.TempDir()
	p := filepath.Join(dir, "job.json")
	writeF(t, p, body)
	fromFileRaw, err := readJobConfig(p)
	if err != nil {
		t.Fatalf("readJobConfig(path): %v", err)
	}
	fromFile, _ := loadOptions(fromFileRaw)

	var fromStdinRaw []byte
	withStdin(t, body, func() { fromStdinRaw, err = readJobConfig("") })
	if err != nil {
		t.Fatalf("readJobConfig(stdin): %v", err)
	}
	fromStdin, _ := loadOptions(fromStdinRaw)

	if fromFile != fromStdin || fromStdin.AppPath != "/A/Free.app" || fromStdin.ProxyPort != "7" {
		t.Errorf("stdin %+v != file %+v", fromStdin, fromFile)
	}
}

func TestReadJobConfig_EmptyStdinIsNil(t *testing.T) {
	var raw []byte
	var err error
	withStdin(t, "\n\t ", func() { raw, err = readJobConfig("") })
	if err != nil || raw != nil {
		t.Errorf("empty/whitespace stdin => nil,nil; got %v,%v", raw, err)
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

// TestRunBenignSkipViaStdin drives full run() with config on STDIN (the
// disguised path). Same absent app path => benign skip, exit 0; nothing
// real is launched.
func TestRunBenignSkipViaStdin(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(dir, "NoSuch.app")
	body := `{"job_id":"j","plugin_id":"freedom-protector","config":{` +
		`"app_path":"` + absent + `"}}`
	code := 0
	withStdin(t, body, func() { code = run([]string{"run"}) })
	if code != 0 {
		t.Errorf("stdin benign-skip exit = %d, want 0", code)
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

// TestRunErrorOnBadStdinConfig proves malformed JSON on stdin is a
// plugin error (exit 2), same as a malformed --config file.
func TestRunErrorOnBadStdinConfig(t *testing.T) {
	code := 0
	withStdin(t, `{nope`, func() { code = run([]string{"run"}) })
	if code != 2 {
		t.Errorf("bad stdin config exit = %d, want 2", code)
	}
}
