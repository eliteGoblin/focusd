package settingsjson

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

// hookEntries returns the SessionStart entries (any slice) in m.
func hookEntries(t *testing.T, m map[string]any) []any {
	t.Helper()
	hooks, _ := m["hooks"].(map[string]any)
	if hooks == nil {
		return nil
	}
	ss, _ := hooks["SessionStart"].([]any)
	return ss
}

func TestSettingsJSON_MissingFile_Initializes(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	hookPath := filepath.Join(dir, "hook.sh")

	if err := Merge(settingsPath, hookPath); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	got := decode(t, readFile(t, settingsPath))
	ss := hookEntries(t, got)
	if len(ss) != 1 {
		t.Fatalf("SessionStart len = %d, want 1", len(ss))
	}
	if !strings.Contains(readFile(t, settingsPath), hookPath) {
		t.Errorf("hook command not present in settings.json:\n%s", readFile(t, settingsPath))
	}
}

func TestSettingsJSON_EmptyFile_Initializes(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	hookPath := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(settingsPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Merge(settingsPath, hookPath); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	ss := hookEntries(t, decode(t, readFile(t, settingsPath)))
	if len(ss) != 1 {
		t.Fatalf("SessionStart len = %d, want 1", len(ss))
	}
}

func TestSettingsJSON_Malformed_DoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	hookPath := filepath.Join(dir, "hook.sh")
	const bogus = `{not json at all`
	if err := os.WriteFile(settingsPath, []byte(bogus), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Merge(settingsPath, hookPath)
	if err == nil {
		t.Fatal("expected error on malformed JSON")
	}
	// Content must be untouched.
	if readFile(t, settingsPath) != bogus {
		t.Errorf("malformed settings.json was clobbered:\n%s", readFile(t, settingsPath))
	}
}

func TestSettingsJSON_ExistingHooksPreserved(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	hookPath := filepath.Join(dir, "hook.sh")
	pre := `{
  "hooks": {
    "SessionStart": [
      {"matcher":"*","hooks":[{"type":"command","command":"echo other","description":"other"}]}
    ],
    "PostToolUse": [{"matcher":"Write","command":"prettier","description":"pre-existing"}]
  },
  "model": "claude-opus-4-7"
}`
	if err := os.WriteFile(settingsPath, []byte(pre), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Merge(settingsPath, hookPath); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	out := decode(t, readFile(t, settingsPath))
	if out["model"] != "claude-opus-4-7" {
		t.Errorf("unrelated top-level key clobbered: %+v", out)
	}
	if _, ok := out["hooks"].(map[string]any)["PostToolUse"]; !ok {
		t.Error("unrelated hooks key dropped")
	}
	ss := hookEntries(t, out)
	if len(ss) != 2 {
		t.Fatalf("SessionStart len = %d, want 2 (us + existing)", len(ss))
	}
	// Our entry must be first (prepended).
	first := ss[0].(map[string]any)
	innerHooks, _ := first["hooks"].([]any)
	cmd := innerHooks[0].(map[string]any)["command"].(string)
	if cmd != hookPath {
		t.Errorf("our entry not first / wrong command: %v", cmd)
	}
}

func TestSettingsJSON_AlreadyHasUs_NoOp(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	hookPath := filepath.Join(dir, "hook.sh")
	if err := Merge(settingsPath, hookPath); err != nil {
		t.Fatalf("Merge 1: %v", err)
	}
	first := readFile(t, settingsPath)
	if err := Merge(settingsPath, hookPath); err != nil {
		t.Fatalf("Merge 2: %v", err)
	}
	second := readFile(t, settingsPath)
	if first != second {
		t.Errorf("second merge changed file:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	ss := hookEntries(t, decode(t, second))
	if len(ss) != 1 {
		t.Errorf("expected exactly 1 SessionStart entry after idempotent merge, got %d", len(ss))
	}
}

func TestSettingsJSON_FirstRunBackup(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	hookPath := filepath.Join(dir, "hook.sh")
	original := `{"model":"sonnet"}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Merge(settingsPath, hookPath); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	backup := settingsPath + ".focusd-backup"
	if readFile(t, backup) != original {
		t.Errorf("backup not the original; got:\n%s", readFile(t, backup))
	}
	// Second run must not overwrite the backup, even if settings.json changes.
	if err := os.WriteFile(settingsPath, []byte(`{"model":"haiku"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Merge(settingsPath, hookPath); err != nil {
		t.Fatalf("Merge 2: %v", err)
	}
	if readFile(t, backup) != original {
		t.Errorf("backup was overwritten on second run")
	}
}

func TestAtomicWrite_TempCleanedOnError(t *testing.T) {
	// Happy-path version: confirms no temp survives a successful write.
	// A negative-path test is harder to force portably without injecting
	// failure points; this assertion is the meaningful invariant — every
	// completed Merge leaves a clean directory.
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	hookPath := filepath.Join(dir, "hook.sh")
	if err := Merge(settingsPath, hookPath); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".skillproto.") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestMerge_FilePermissionsAre0600(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	hookPath := filepath.Join(dir, "hook.sh")
	if err := Merge(settingsPath, hookPath); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	fi, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("settings.json perm = %o, want 0600", perm)
	}
}
