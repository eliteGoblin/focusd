package osadapter

import (
	"strings"
	"testing"
)

// TestWorkdirEnvKeyOpaque: the key that carries the workdir off-argv must not
// itself name focusd or 'workdir' (it appears in the plist environment). It must
// also match the daemon side (platformsvc.WorkdirEnvKey) — asserted here by
// pinning the literal, since the two live in separate modules.
func TestWorkdirEnvKeyOpaque(t *testing.T) {
	if WorkdirEnvKey != "APP_STATE_DIR" {
		t.Fatalf("WorkdirEnvKey drifted from the daemon's literal: %q", WorkdirEnvKey)
	}
	low := strings.ToLower(WorkdirEnvKey)
	for _, banned := range []string{"focusd", "workdir", "platform"} {
		if strings.Contains(low, banned) {
			t.Errorf("WorkdirEnvKey leaks %q: %s", banned, WorkdirEnvKey)
		}
	}
}

// TestRandomPluginArgv0ZeroLeak: every disguised plugin argv[0] token must be a
// clean single token that reveals no plugin identity — no id, no 'focusd', no
// 'platform', no path/flag characters. Sampled heavily since it is random.
func TestRandomPluginArgv0ZeroLeak(t *testing.T) {
	pluginIDs := []string{"kill-steam", "dns-block", "network-block", "skill-protector", "browser-monitor", "freedom-protector"}
	banned := append([]string{"focusd", "platform", "--config", "/"}, pluginIDs...)
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		tok := RandomPluginArgv0()
		seen[tok] = true
		if tok == "" || strings.ContainsAny(tok, "/. -") {
			t.Fatalf("plugin argv0 token %q is not a clean single token", tok)
		}
		low := strings.ToLower(tok)
		for _, b := range banned {
			if strings.Contains(low, strings.ToLower(b)) {
				t.Fatalf("plugin argv0 token %q leaks %q", tok, b)
			}
		}
	}
	// Sanity: the pool actually varies (not a constant).
	if len(seen) < 3 {
		t.Errorf("RandomPluginArgv0 produced only %d distinct tokens over 2000 draws", len(seen))
	}
}

// TestPluginProcTokensNoIDSubstring: no pool token may itself contain a plugin id
// fragment ('steam', 'dns', 'block', 'kill', 'browser', 'skill', 'freedom').
func TestPluginProcTokensNoIDSubstring(t *testing.T) {
	frags := []string{"steam", "dns", "block", "kill", "browser", "skill", "freedom", "focusd", "platform"}
	for _, tok := range pluginProcTokens {
		low := strings.ToLower(tok)
		for _, f := range frags {
			if strings.Contains(low, f) {
				t.Errorf("pluginProcTokens entry %q contains id fragment %q", tok, f)
			}
		}
	}
}
