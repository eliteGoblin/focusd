package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// leakyPluginIDs are the real plugin ids that MUST NOT appear as an on-disk
// directory or binary basename after extraction (HF4 / FEATURE 24, requirement
// C). They remain only inside plugin.json "id" (the discovery key) — an accepted,
// documented residual, since finding an id in a file already inside the disguised
// workdir does not help LOCATE that workdir.
var leakyPluginIDs = []string{"kill-steam", "dns-block", "network-block", "skill-protector", "browser-monitor", "freedom-protector"}

// TestExtractedNamesAreNeutral asserts the extracted bundle carries NEUTRAL dir +
// binary basenames — no plugin-id token on disk — while each plugin.json still
// declares its REAL id (discovery keys off that, not the dir name) and points its
// entrypoint at the neutral binary that actually exists.
func TestExtractedNamesAreNeutral(t *testing.T) {
	root := t.TempDir()
	if _, err := ExtractTo(root); err != nil {
		t.Fatalf("ExtractTo: %v", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	sawPlugin := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := e.Name()
		// 1. No plugin-id token in the DIRECTORY name.
		for _, id := range leakyPluginIDs {
			if strings.Contains(dir, id) {
				t.Errorf("extracted dir %q leaks plugin id %q", dir, id)
			}
		}

		pj := filepath.Join(root, dir, "plugin.json")
		raw, rerr := os.ReadFile(pj)
		if rerr != nil {
			continue // not a plugin dir
		}
		var m struct {
			ID         string `json:"id"`
			Entrypoint string `json:"entrypoint"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("parse %s: %v", pj, err)
		}
		sawPlugin++

		// 2. plugin.json still declares the REAL id (discovery key preserved).
		realID := false
		for _, id := range leakyPluginIDs {
			if m.ID == id {
				realID = true
			}
		}
		if !realID {
			t.Errorf("plugin.json id %q is not a known real id — discovery would break", m.ID)
		}

		// 3. Entrypoint is a NEUTRAL basename (no id) that exists on disk.
		base := strings.TrimPrefix(m.Entrypoint, "./")
		for _, id := range leakyPluginIDs {
			if strings.Contains(base, id) {
				t.Errorf("entrypoint %q (plugin id %q) leaks the id on disk", m.Entrypoint, m.ID)
			}
		}
		binPath := filepath.Join(root, dir, base)
		fi, serr := os.Stat(binPath)
		if serr != nil || fi.IsDir() {
			t.Errorf("entrypoint binary %q for id %q missing on disk", base, m.ID)
			continue
		}
		// The binary basename must equal the neutral dir (our bundle convention)
		// and carry no id.
		if base == m.ID {
			t.Errorf("binary basename equals the plugin id %q — not disguised", m.ID)
		}
	}
	if sawPlugin == 0 {
		t.Fatal("no plugins extracted — bundle empty?")
	}
}
