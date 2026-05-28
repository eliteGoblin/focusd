package reconciler

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustReadFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return b
}

func sumHex(b []byte) string {
	h := sha256.Sum256(b)
	return string(h[:])
}

// expectedPaths returns the three target file paths relative to home.
func expectedPaths(home string) (skill, rule, hook, settings string) {
	skill = filepath.Join(home, ".claude", "skills", "focusd-protection", "SKILL.md")
	rule = filepath.Join(home, ".claude", "rules", "frank", "focusd-protection.md")
	hook = filepath.Join(home, ".claude", "hooks", "focusd-protection-reinject.sh")
	settings = filepath.Join(home, ".claude", "settings.json")
	return
}

func TestReconcile_AllAbsent_CreatesAll(t *testing.T) {
	home := t.TempDir()
	r := New(home)

	out, err := r.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	skill, rule, hook, settings := expectedPaths(home)
	for _, p := range []string{skill, rule, hook, settings} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist: %v", p, err)
		}
	}
	if len(out.Written) != 3 {
		t.Errorf("Written = %v, want 3 entries", out.Written)
	}
	if out.SettingsStatus != "ok" {
		t.Errorf("SettingsStatus = %q, want ok", out.SettingsStatus)
	}
}

func TestReconcile_AllMatch_NoWrite(t *testing.T) {
	home := t.TempDir()
	r := New(home)
	if _, err := r.Reconcile(); err != nil {
		t.Fatalf("seed Reconcile: %v", err)
	}
	skill, rule, hook, _ := expectedPaths(home)
	// Snapshot mtimes.
	stat := func(p string) (os.FileInfo, []byte) {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return fi, mustReadFile(t, p)
	}
	skillFi, skillBefore := stat(skill)
	ruleFi, ruleBefore := stat(rule)
	hookFi, hookBefore := stat(hook)

	out, err := r.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile 2: %v", err)
	}
	if len(out.Written) != 0 {
		t.Errorf("expected zero writes on second pass, got %v", out.Written)
	}
	if out.Noop != 3 {
		t.Errorf("Noop = %d, want 3", out.Noop)
	}
	// Inodes/content must be identical.
	for _, c := range []struct {
		name         string
		before, want []byte
		path         string
		prev         os.FileInfo
	}{
		{"skill", skillBefore, skillBefore, skill, skillFi},
		{"rule", ruleBefore, ruleBefore, rule, ruleFi},
		{"hook", hookBefore, hookBefore, hook, hookFi},
	} {
		after := mustReadFile(t, c.path)
		if sumHex(after) != sumHex(c.want) {
			t.Errorf("%s: content changed", c.name)
		}
	}
}

func TestReconcile_DriftSkill_Rewrites(t *testing.T) {
	home := t.TempDir()
	r := New(home)
	if _, err := r.Reconcile(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	skill, _, _, _ := expectedPaths(home)
	if err := os.WriteFile(skill, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := r.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile after drift: %v", err)
	}
	if !contains(out.Written, skill) {
		t.Errorf("expected skill rewrite, got Written=%v", out.Written)
	}
	got := mustReadFile(t, skill)
	if string(got) == "tampered" {
		t.Error("skill was not rewritten")
	}
}

func TestReconcile_MissingParentDir_Creates(t *testing.T) {
	home := t.TempDir()
	// Do not pre-create any of the .claude tree; reconciler must mkdir.
	r := New(home)
	if _, err := r.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	skill, rule, hook, _ := expectedPaths(home)
	for _, p := range []string{skill, rule, hook} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s not created: %v", p, err)
		}
	}
}

func TestReconcile_HookHasExecBit(t *testing.T) {
	home := t.TempDir()
	r := New(home)
	if _, err := r.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	_, _, hook, _ := expectedPaths(home)
	fi, err := os.Stat(hook)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("hook perm = %o, want 0700", fi.Mode().Perm())
	}
}

func TestReconcile_SettingsHasOurHookCommand(t *testing.T) {
	home := t.TempDir()
	r := New(home)
	if _, err := r.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	_, _, hook, settings := expectedPaths(home)
	data := mustReadFile(t, settings)
	var top map[string]any
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("settings.json not valid JSON: %v\n%s", err, data)
	}
	if !strings.Contains(string(data), hook) {
		t.Errorf("hook path not present in settings.json:\n%s", data)
	}
}

func TestReconcile_MalformedSettingsReportsError(t *testing.T) {
	home := t.TempDir()
	_, _, _, settings := expectedPaths(home)
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := New(home)
	out, err := r.Reconcile()
	// Three content files still written; settings reported as error.
	if err == nil {
		t.Error("expected reconciler to surface a settings error")
	}
	if len(out.Written) != 3 {
		t.Errorf("expected 3 content files written despite settings error, got %v", out.Written)
	}
	if out.SettingsStatus != "error" {
		t.Errorf("SettingsStatus = %q, want error", out.SettingsStatus)
	}
}

func TestReconcile_AtomicWriteNoTempLeftBehind(t *testing.T) {
	home := t.TempDir()
	r := New(home)
	if _, err := r.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	dirs := []string{
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".claude", "skills", "focusd-protection"),
		filepath.Join(home, ".claude", "rules", "frank"),
		filepath.Join(home, ".claude", "hooks"),
	}
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatalf("readdir %s: %v", d, err)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".skillproto.") {
				t.Errorf("temp file left in %s: %s", d, e.Name())
			}
		}
	}
}

// Security-reviewer MEDIUM: refuse to write under a symlinked
// ~/.claude. The user-as-attacker model includes pre-planting a
// symlink to a privileged path; we must Lstat and reject.
func TestReconcile_ClaudeDirIsSymlink_Refuses(t *testing.T) {
	home := t.TempDir()
	target := t.TempDir() // any other real dir
	symPath := filepath.Join(home, ".claude")
	if err := os.Symlink(target, symPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	r := New(home)
	_, err := r.Reconcile()
	if err == nil {
		t.Fatal("Reconcile accepted symlinked ~/.claude; should have refused")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink: %v", err)
	}
	// Belt-and-braces: no files written to the target dir.
	entries, _ := os.ReadDir(target)
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("symlink target was written to: %v", names)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
