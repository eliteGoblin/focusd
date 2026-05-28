// Package settingsjson safely merges the focusd SessionStart hook into
// the user's ~/.claude/settings.json without clobbering existing keys.
//
// Safety rules:
//   - Refuse to touch a file that does not parse as JSON. The user must
//     fix it manually before we can re-merge.
//   - Preserve every existing top-level key, every existing hooks key,
//     and every existing SessionStart entry.
//   - Idempotent: if our hook command is already present, no write.
//   - On the first successful merge, copy the original file to
//     "<path>.focusd-backup" — one-shot, never overwritten.
//   - Atomic write: temp file in the same directory, fsync, chmod 0600,
//     rename. On any error after CreateTemp, the temp is removed.
package settingsjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// hookDescription is the marker we tag our SessionStart entry with so
// future maintainers can spot it.
const hookDescription = "focusd-protection reinject"

// Merge ensures settingsPath contains a SessionStart hook invoking
// absHookPath. settingsPath is created if missing. absHookPath is
// recorded verbatim; the caller must pass an absolute path.
func Merge(settingsPath, absHookPath string) error {
	if !filepath.IsAbs(absHookPath) {
		return fmt.Errorf("hook path must be absolute: %q", absHookPath)
	}

	raw, readErr := os.ReadFile(settingsPath)
	missing := errors.Is(readErr, fs.ErrNotExist)
	if readErr != nil && !missing {
		return fmt.Errorf("read settings.json: %w", readErr)
	}

	var top map[string]any
	if missing || len(raw) == 0 {
		top = map[string]any{}
	} else {
		if err := json.Unmarshal(raw, &top); err != nil {
			return fmt.Errorf("settings.json malformed, refusing to write; "+
				"user must fix manually: %w", err)
		}
		if top == nil {
			top = map[string]any{}
		}
	}

	hooks, _ := top["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	ss, _ := hooks["SessionStart"].([]any)

	// Idempotency check: walk every existing entry and every inner hook;
	// if our absolute command is already present, no write needed.
	if hasOurHook(ss, absHookPath) {
		return nil
	}

	ourEntry := map[string]any{
		"matcher": "*",
		"hooks": []any{
			map[string]any{
				"type":        "command",
				"command":     absHookPath,
				"description": hookDescription,
			},
		},
	}
	ss = append([]any{ourEntry}, ss...)
	hooks["SessionStart"] = ss
	top["hooks"] = hooks

	data, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings.json: %w", err)
	}
	data = append(data, '\n')

	// One-shot backup of the original (only when we have one and the
	// backup does not already exist). Best-effort: failure to create
	// the backup does NOT block the merge.
	if !missing && len(raw) > 0 {
		backup := settingsPath + ".focusd-backup"
		if _, err := os.Stat(backup); errors.Is(err, fs.ErrNotExist) {
			_ = atomicWrite(backup, raw, 0o600)
		}
	}

	return atomicWrite(settingsPath, data, 0o600)
}

// hasOurHook returns true when our absolute command appears anywhere
// inside the SessionStart slice.
func hasOurHook(entries []any, cmd string) bool {
	for _, e := range entries {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		inner, ok := em["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range inner {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if hm["command"] == cmd {
				return true
			}
		}
	}
	return false
}

// atomicWrite writes data to path via a temp file in the same dir,
// chmod-ing to perm before rename. On any error after CreateTemp, the
// temp file is removed so no partials remain.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".skillproto.")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	clean := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		clean()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		clean()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		clean()
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		clean()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		clean()
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	return nil
}
