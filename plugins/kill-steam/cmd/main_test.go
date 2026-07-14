package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeF writes a test fixture file, failing fast on I/O error so a
// real failure is diagnosable (not masked behind a later assertion).
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
	defer func() { os.Stdin = old }()
	fn()
}

func TestLoadNamesDefaultsWhenEmpty(t *testing.T) {
	n, err := loadNames(nil)
	if err != nil || n != nil {
		t.Errorf("nil raw => nil names,nil err; got %v,%v", n, err)
	}
}

func TestLoadNamesFromConfig(t *testing.T) {
	raw := []byte(`{"job_id":"j","plugin_id":"kill-steam","config":{"process_names":["Foo","Bar"]}}`)
	n, err := loadNames(raw)
	if err != nil {
		t.Fatalf("loadNames: %v", err)
	}
	if len(n) != 2 || n[0] != "Foo" || n[1] != "Bar" {
		t.Errorf("got %v", n)
	}
}

func TestLoadNamesNoProcessNamesKey(t *testing.T) {
	n, err := loadNames([]byte(`{"job_id":"j","config":{}}`))
	if err != nil || n != nil {
		t.Errorf("missing key => nil,nil; got %v,%v", n, err)
	}
}

func TestLoadNamesErrors(t *testing.T) {
	if _, err := loadNames([]byte(`{not json`)); err == nil {
		t.Error("expected parse error")
	}
	if _, err := loadNames([]byte(`{"config":{"process_names":"notalist"}}`)); err == nil {
		t.Error("expected type error for non-list process_names")
	}
	if _, err := loadNames([]byte(`{"config":{"process_names":[1,2]}}`)); err == nil {
		t.Error("expected type error for non-string entries")
	}
}

// --- readJobConfig: --config (compat) vs stdin (disguised) ---

func TestReadJobConfigCompatAndStdinMatch(t *testing.T) {
	body := `{"config":{"process_names":["Zed"]}}`

	// Compat path: --config <file>.
	dir := t.TempDir()
	p := filepath.Join(dir, "job.json")
	writeF(t, p, body)
	fromFile, err := readJobConfig(p)
	if err != nil {
		t.Fatalf("readJobConfig(path): %v", err)
	}

	// Disguised path: stdin.
	var fromStdin []byte
	withStdin(t, body, func() { fromStdin, err = readJobConfig("") })
	if err != nil {
		t.Fatalf("readJobConfig(stdin): %v", err)
	}

	nf, _ := loadNames(fromFile)
	ns, _ := loadNames(fromStdin)
	if len(nf) != 1 || len(ns) != 1 || nf[0] != "Zed" || ns[0] != "Zed" {
		t.Errorf("stdin/file mismatch: file=%v stdin=%v", nf, ns)
	}
}

func TestReadJobConfigEmptyStdinIsDefaults(t *testing.T) {
	var raw []byte
	var err error
	withStdin(t, "", func() { raw, err = readJobConfig("") })
	if err != nil || raw != nil {
		t.Errorf("empty stdin => nil,nil; got %v,%v", raw, err)
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

func TestRunHappyPathKillsNothing(t *testing.T) {
	// Real run with a process name that cannot match -> scans real
	// processes, kills none, exits 0. No real process is harmed.
	dir := t.TempDir()
	cfg := filepath.Join(dir, "job.json")
	writeF(t, cfg, `{"job_id":"j","plugin_id":"kill-steam","config":{"process_names":["zzz-focusd-test-nonexistent"]}}`)
	if code := run([]string{"run", "--config", cfg}); code != 0 {
		t.Errorf("happy path exit = %d, want 0", code)
	}
}

// TestRunHappyPathViaStdin drives full run() with config on STDIN (the
// disguised path). Same non-matching process name => nothing is killed.
func TestRunHappyPathViaStdin(t *testing.T) {
	body := `{"job_id":"j","plugin_id":"kill-steam","config":{"process_names":["zzz-focusd-test-nonexistent"]}}`
	code := 0
	withStdin(t, body, func() { code = run([]string{"run"}) })
	if code != 0 {
		t.Errorf("stdin happy path exit = %d, want 0", code)
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
