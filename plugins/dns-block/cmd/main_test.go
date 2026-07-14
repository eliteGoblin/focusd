package main

import (
	"os"
	"path/filepath"
	"testing"
)

// withStdin swaps os.Stdin for a pipe carrying data for the duration of
// fn, then restores it. Used to exercise the disguised (stdin) config path.
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

// --- loadHostsOverride (bytes) ---
//
// dns-block's run() reconciles /etc/hosts (a destructive, root-requiring
// side effect), so we unit-test the refactored bytes helper + the stdin
// drain directly rather than driving full run().

func TestLoadHostsOverrideDefaultsWhenEmpty(t *testing.T) {
	// nil/empty raw => embedded blocklist (nil, nil). Whitespace-only
	// never reaches here: readJobConfig trims it to nil first.
	for _, raw := range [][]byte{nil, {}} {
		h, err := loadHostsOverride(raw)
		if err != nil || h != nil {
			t.Errorf("empty raw => nil,nil; got %v,%v", h, err)
		}
	}
}

func TestLoadHostsOverrideFromBytes(t *testing.T) {
	raw := []byte(`{"job_id":"j","plugin_id":"dns-block","config":{"hosts":["a.com","b.com"]}}`)
	h, err := loadHostsOverride(raw)
	if err != nil {
		t.Fatalf("loadHostsOverride: %v", err)
	}
	if len(h) != 2 || h[0] != "a.com" || h[1] != "b.com" {
		t.Errorf("got %v", h)
	}
}

func TestLoadHostsOverrideNoHostsKey(t *testing.T) {
	h, err := loadHostsOverride([]byte(`{"config":{}}`))
	if err != nil || h != nil {
		t.Errorf("missing key => nil,nil; got %v,%v", h, err)
	}
}

func TestLoadHostsOverrideErrors(t *testing.T) {
	if _, err := loadHostsOverride([]byte(`{not json`)); err == nil {
		t.Error("expected parse error")
	}
	if _, err := loadHostsOverride([]byte(`{"config":{"hosts":"notarray"}}`)); err == nil {
		t.Error("expected type error for non-array hosts")
	}
	if _, err := loadHostsOverride([]byte(`{"config":{"hosts":[1,2]}}`)); err == nil {
		t.Error("expected type error for non-string entries")
	}
}

// --- readJobConfig: --config (compat) vs stdin (disguised) ---

func TestReadJobConfigFromFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "job.json")
	body := `{"config":{"hosts":["x.com"]}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := readJobConfig(p)
	if err != nil {
		t.Fatalf("readJobConfig(path): %v", err)
	}
	if string(raw) != body {
		t.Errorf("got %q", raw)
	}
}

func TestReadJobConfigFromStdin(t *testing.T) {
	body := `{"config":{"hosts":["stdin.example"]}}`
	var raw []byte
	var err error
	withStdin(t, body, func() { raw, err = readJobConfig("") })
	if err != nil {
		t.Fatalf("readJobConfig(stdin): %v", err)
	}
	if string(raw) != body {
		t.Errorf("stdin bytes = %q, want %q", raw, body)
	}
	// And it must parse identically to the --config path.
	h, err := loadHostsOverride(raw)
	if err != nil || len(h) != 1 || h[0] != "stdin.example" {
		t.Errorf("stdin config not honored: %v,%v", h, err)
	}
}

func TestReadJobConfigEmptyStdinIsDefaults(t *testing.T) {
	var raw []byte
	var err error
	withStdin(t, "   \n\t", func() { raw, err = readJobConfig("") })
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
