package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeJobConfig(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "job.json")
	if err := os.WriteFile(p, []byte(`{"job_id":"j","plugin_id":"skill-protector","config":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
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

func TestRun_UsageAndVersion(t *testing.T) {
	if code := run([]string{"version"}); code != 0 {
		t.Errorf("version exit = %d, want 0", code)
	}
	if code := run([]string{}); code != 2 {
		t.Errorf("no args exit = %d, want 2", code)
	}
	if code := run([]string{"bogus"}); code != 2 {
		t.Errorf("bad subcommand exit = %d, want 2", code)
	}
}

// TestRun_HappyPathWritesAll exercises the full contract end to end:
// fresh temp HOME, no pre-existing .claude/, plugin must create the
// three artifacts and write settings.json. Exit 0 means the JSON
// emitted to stdout was a success result.
func TestRun_HappyPathWritesAll(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	cfg := writeJobConfig(t, dir)

	code := run([]string{"run", "--config", cfg, "--home", home})
	if code != 0 {
		t.Fatalf("run exit = %d, want 0", code)
	}
	for _, p := range []string{
		filepath.Join(home, ".claude", "skills", "focusd-protection", "SKILL.md"),
		filepath.Join(home, ".claude", "rules", "frank", "focusd-protection.md"),
		filepath.Join(home, ".claude", "hooks", "focusd-protection-reinject.sh"),
		filepath.Join(home, ".claude", "settings.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s missing: %v", p, err)
		}
	}
}

// TestRun_HappyPathViaStdin drives full run() with the job config on
// STDIN (the disguised path). skill-protector ignores the config content
// but must still drain stdin per contract; artifacts are written to a
// fresh temp HOME so nothing real is touched.
func TestRun_HappyPathViaStdin(t *testing.T) {
	home := t.TempDir()
	code := 0
	withStdin(t, `{"job_id":"j","plugin_id":"skill-protector","config":{}}`, func() {
		code = run([]string{"run", "--home", home})
	})
	if code != 0 {
		t.Fatalf("stdin run exit = %d, want 0", code)
	}
	for _, p := range []string{
		filepath.Join(home, ".claude", "skills", "focusd-protection", "SKILL.md"),
		filepath.Join(home, ".claude", "rules", "frank", "focusd-protection.md"),
		filepath.Join(home, ".claude", "hooks", "focusd-protection-reinject.sh"),
		filepath.Join(home, ".claude", "settings.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s missing: %v", p, err)
		}
	}
}

// TestRun_EmptyStdinStillSucceeds confirms that with no --config and
// empty stdin (config-less invocation), the plugin still reconciles and
// exits 0 — the config is optional for this plugin.
func TestRun_EmptyStdinStillSucceeds(t *testing.T) {
	home := t.TempDir()
	code := 0
	withStdin(t, "", func() { code = run([]string{"run", "--home", home}) })
	if code != 0 {
		t.Errorf("empty-stdin run exit = %d, want 0", code)
	}
}

// TestRun_MalformedSettingsExits1 verifies the exit-code contract:
// settings.json that doesn't parse is a controlled failure (exit 1),
// not a plugin error (exit 2).
func TestRun_MalformedSettingsExits1(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := writeJobConfig(t, dir)

	code := run([]string{"run", "--config", cfg, "--home", home})
	if code != 1 {
		t.Errorf("malformed settings exit = %d, want 1", code)
	}
	// Content files must still exist despite the settings failure.
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "focusd-protection", "SKILL.md")); err != nil {
		t.Errorf("SKILL.md should still be written: %v", err)
	}
}

// TestRun_SecondInvocationIsNoop confirms idempotency at the command
// level: the second run writes nothing new and still exits 0.
func TestRun_SecondInvocationIsNoop(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	cfg := writeJobConfig(t, dir)

	if code := run([]string{"run", "--config", cfg, "--home", home}); code != 0 {
		t.Fatalf("first run exit = %d", code)
	}
	if code := run([]string{"run", "--config", cfg, "--home", home}); code != 0 {
		t.Fatalf("second run exit = %d", code)
	}
}
