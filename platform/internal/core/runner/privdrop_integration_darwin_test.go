//go:build darwin

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/platform/internal/core/plugin"
	"github.com/eliteGoblin/focusd/platform/internal/core/state"
	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
	"github.com/eliteGoblin/focusd/platform/internal/testutil"
)

// TestPrivDropExecAsConsoleUser is the real fork→setuid integration check.
// It ONLY runs when the test process is actually root on a real Mac with a
// console user logged in — otherwise the kernel won't honor the Credential.
// CI (non-root) and root-but-loginwindow both skip cleanly.
//
// It runs a stub plugin under a system-mode runner and asserts the child
// observed: euid == console uid, HOME == the user's home, and a TMPDIR it
// can actually write to (the three silent-corruption edges, end to end).
func TestPrivDropExecAsConsoleUser(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("privilege-drop integration test requires root (not root)")
	}
	uid, gid, name, home, err := realConsoleUser()
	if err != nil {
		t.Skipf("cannot discover console user: %v", err)
	}
	if uid == 0 {
		t.Skip("no console user logged in (loginwindow / fast-user-switch)")
	}
	_ = gid
	_ = name

	db, err := state.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// The stub writes its observed euid, $HOME, and a temp-file-creation
	// probe into an output file the test reads back. Make the output dir
	// world-writable so the dropped uid can write it.
	outDir := t.TempDir()
	if err := os.Chmod(outDir, 0o777); err != nil {
		t.Fatalf("chmod outdir: %v", err)
	}
	outFile := filepath.Join(outDir, "observed.txt")

	script := strings.Join([]string{
		`euid=$(id -u)`,
		`# Probe TMPDIR writability (root's TMPDIR would be unwritable here).`,
		`if t=$(mktemp 2>/dev/null); then tmpok=YES; rm -f "$t"; else tmpok=NO; fi`,
		`printf 'euid=%s\nhome=%s\ntmpok=%s\n' "$euid" "$HOME" "$tmpok" > '` + outFile + `'`,
		`echo '{"status":"ok"}'`,
	}, "\n")

	p := testutil.ScriptPlugin(t, "skill-protector", script)
	p.Manifest.RunAs = plugin.RunAsCurrentUser

	r := NewWithMode(db, osadapter.ModeSystem)
	out, err := r.Run(context.Background(), Job{ID: "j1"}, p, "scheduler")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Status != state.RunStatusOK {
		t.Fatalf("status = %q (stderr=%q)", out.Status, out.Stderr)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read observed output: %v", err)
	}
	obs := parseKV(string(data))

	if obs["euid"] != strconv.Itoa(uid) {
		t.Errorf("child euid = %q, want console uid %d", obs["euid"], uid)
	}
	if obs["home"] != home {
		t.Errorf("child HOME = %q, want %q", obs["home"], home)
	}
	if obs["tmpok"] != "YES" {
		t.Errorf("child could not create a temp file (TMPDIR reseed broken): %q", obs["tmpok"])
	}
}

func parseKV(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if i := strings.IndexByte(line, '='); i >= 0 {
			m[line[:i]] = line[i+1:]
		}
	}
	return m
}
