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

func TestLoadNamesDefaultsWhenNoPath(t *testing.T) {
	n, err := loadNames("")
	if err != nil || n != nil {
		t.Errorf("empty path => nil names,nil err; got %v,%v", n, err)
	}
}

func TestLoadNamesFromConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "job.json")
	writeF(t, p, `{"job_id":"j","plugin_id":"kill-steam","config":{"process_names":["Foo","Bar"]}}`)
	n, err := loadNames(p)
	if err != nil {
		t.Fatalf("loadNames: %v", err)
	}
	if len(n) != 2 || n[0] != "Foo" || n[1] != "Bar" {
		t.Errorf("got %v", n)
	}
}

func TestLoadNamesNoProcessNamesKey(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "job.json")
	writeF(t, p, `{"job_id":"j","config":{}}`)
	n, err := loadNames(p)
	if err != nil || n != nil {
		t.Errorf("missing key => nil,nil; got %v,%v", n, err)
	}
}

func TestLoadNamesErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	writeF(t, bad, `{not json`)
	if _, err := loadNames(bad); err == nil {
		t.Error("expected parse error")
	}
	if _, err := loadNames(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("expected read error")
	}

	wrongType := filepath.Join(dir, "wt.json")
	writeF(t, wrongType, `{"config":{"process_names":"notalist"}}`)
	if _, err := loadNames(wrongType); err == nil {
		t.Error("expected type error for non-list process_names")
	}

	wrongElem := filepath.Join(dir, "we.json")
	writeF(t, wrongElem, `{"config":{"process_names":[1,2]}}`)
	if _, err := loadNames(wrongElem); err == nil {
		t.Error("expected type error for non-string entries")
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

func TestRunErrorOnBadConfig(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	writeF(t, bad, `{nope`)
	if code := run([]string{"run", "--config", bad}); code != 2 {
		t.Errorf("bad config exit = %d, want 2", code)
	}
}
