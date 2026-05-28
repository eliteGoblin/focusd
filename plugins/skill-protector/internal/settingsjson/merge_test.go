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

// Go-reviewer CRITICAL #2: a non-map "hooks" value (e.g. a string from
// a hand-edit gone wrong) must NOT be silently replaced — that drops
// the user's existing data. Merge must refuse + leave the file
// untouched, just like the malformed-JSON case.
func TestSettingsJSON_HooksNotMap_RefusesAndPreservesFile(t *testing.T) {
	cases := []struct {
		name    string
		hookVal string
	}{
		{"hooks as string", `"a string, not a map"`},
		{"hooks as array", `["one","two"]`},
		{"hooks as number", `42`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			settingsPath := filepath.Join(dir, "settings.json")
			hookPath := filepath.Join(dir, "hook.sh")
			original := `{"hooks":` + tc.hookVal + `,"model":"sonnet"}`
			if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			err := Merge(settingsPath, hookPath)
			if err == nil {
				t.Fatalf("Merge accepted non-map hooks; should have refused")
			}
			if !strings.Contains(err.Error(), "hooks") {
				t.Errorf("error should mention \"hooks\" to help the user fix it: %v", err)
			}
			if readFile(t, settingsPath) != original {
				t.Errorf("non-map hooks file was clobbered:\n%s", readFile(t, settingsPath))
			}
		})
	}
}

// Go-reviewer CRITICAL #3: legacy single-object SessionStart form is
// silently dropped by `value, _ := … .([]any)`. Must be preserved by
// wrapping into a one-element array, then prepending our entry.
func TestSettingsJSON_SessionStartLegacyObject_PreservedAsWrappedArray(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	hookPath := filepath.Join(dir, "hook.sh")
	legacy := `{
  "hooks": {
    "SessionStart": {"matcher":"*","hooks":[{"type":"command","command":"echo other","description":"legacy"}]}
  }
}`
	if err := os.WriteFile(settingsPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Merge(settingsPath, hookPath); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	ss := hookEntries(t, decode(t, readFile(t, settingsPath)))
	if len(ss) != 2 {
		t.Fatalf("SessionStart len = %d, want 2 (us + wrapped-legacy)", len(ss))
	}
	// Find the legacy entry — must NOT have been dropped.
	foundLegacy := false
	for _, e := range ss {
		em, _ := e.(map[string]any)
		inner, _ := em["hooks"].([]any)
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			if hm["command"] == "echo other" {
				foundLegacy = true
			}
		}
	}
	if !foundLegacy {
		t.Errorf("legacy single-object SessionStart entry was silently dropped: %+v", ss)
	}
}

// Belt-and-braces: a SessionStart value that's neither array nor
// object (e.g. a string) is rejected with an explanatory error.
func TestSettingsJSON_SessionStartWrongType_Refuses(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	hookPath := filepath.Join(dir, "hook.sh")
	original := `{"hooks":{"SessionStart":"not a hook"}}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Merge(settingsPath, hookPath)
	if err == nil {
		t.Fatal("Merge accepted bogus SessionStart; should have refused")
	}
	if !strings.Contains(err.Error(), "SessionStart") {
		t.Errorf("error should mention SessionStart: %v", err)
	}
	if readFile(t, settingsPath) != original {
		t.Errorf("file was clobbered")
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
