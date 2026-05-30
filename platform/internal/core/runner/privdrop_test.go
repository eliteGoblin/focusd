package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/platform/internal/core/plugin"
	"github.com/eliteGoblin/focusd/platform/internal/core/state"
	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
	"github.com/eliteGoblin/focusd/platform/internal/testutil"
)

// fakeConsoleUser returns a consoleUserFn yielding the given identity.
func fakeConsoleUser(uid, gid int, name, home string, err error) consoleUserFn {
	return func() (int, int, string, string, error) { return uid, gid, name, home, err }
}

// --- resolvePlan matrix -----------------------------------------------------

func TestResolvePlanMatrix(t *testing.T) {
	const (
		uid  = 501
		gid  = 20
		name = "frank"
		home = "/Users/frank"
	)
	cu := fakeConsoleUser(uid, gid, name, home, nil)

	cases := []struct {
		name   string
		mode   osadapter.RunMode
		runAs  string
		want   dropAction
		wantH  string // expected HOME= when dropToUser
		wantTM bool   // expect TMPDIR=/tmp in env when dropToUser
	}{
		{"user platform current_user runs native", osadapter.ModeUser, plugin.RunAsCurrentUser, dropNone, "", false},
		{"user platform system runs native", osadapter.ModeUser, plugin.RunAsSystem, dropNone, "", false},
		{"system platform system runs as root", osadapter.ModeSystem, plugin.RunAsSystem, dropNone, "", false},
		{"system platform current_user drops", osadapter.ModeSystem, plugin.RunAsCurrentUser, dropToUser, "HOME=/Users/frank", true},
		{"system platform active_user drops", osadapter.ModeSystem, plugin.RunAsActiveUser, dropToUser, "HOME=/Users/frank", true},
		{"system platform empty runs as root", osadapter.ModeSystem, "", dropNone, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := resolvePlan(tc.mode, tc.runAs, cu)
			if err != nil {
				t.Fatalf("resolvePlan: %v", err)
			}
			if plan.action != tc.want {
				t.Fatalf("action = %d, want %d", plan.action, tc.want)
			}
			if tc.want == dropToUser {
				if plan.uid != uid || plan.gid != gid {
					t.Errorf("uid/gid = %d/%d, want %d/%d", plan.uid, plan.gid, uid, gid)
				}
				if plan.homeEnv != tc.wantH {
					t.Errorf("homeEnv = %q, want %q", plan.homeEnv, tc.wantH)
				}
			}
		})
	}
}

// EDGE 1 (no console user): console uid 0 → skip, never run as root.
func TestResolvePlanNoConsoleUserSkips(t *testing.T) {
	cu := fakeConsoleUser(0, 0, "", "", nil) // root owns /dev/console
	plan, err := resolvePlan(osadapter.ModeSystem, plugin.RunAsCurrentUser, cu)
	if err != nil {
		t.Fatalf("resolvePlan: %v", err)
	}
	if plan.action != dropSkipNoConsoleUser {
		t.Fatalf("action = %d, want dropSkipNoConsoleUser (%d)", plan.action, dropSkipNoConsoleUser)
	}
}

func TestResolvePlanConsoleUserError(t *testing.T) {
	cu := fakeConsoleUser(0, 0, "", "", errors.New("stat failed"))
	if _, err := resolvePlan(osadapter.ModeSystem, plugin.RunAsCurrentUser, cu); err == nil {
		t.Fatal("expected error to propagate")
	}
}

// EDGE 2 + 3 (HOME + TMPDIR reseed): the dropped child's environment must
// carry the user's HOME and a writable TMPDIR=/tmp (root's would corrupt /
// break the child). Verify the exact env the runner hands to the kernel.
func TestDropEnvReseedsHomeUserLognameTmpdir(t *testing.T) {
	plan, err := resolvePlan(
		osadapter.ModeSystem, plugin.RunAsCurrentUser,
		fakeConsoleUser(501, 20, "frank", "/Users/frank", nil),
	)
	if err != nil {
		t.Fatalf("resolvePlan: %v", err)
	}
	env := plan.dropEnv()
	want := map[string]bool{
		"HOME=/Users/frank": false,
		"USER=frank":        false,
		"LOGNAME=frank":     false,
		"TMPDIR=/tmp":       false,
	}
	for _, e := range env {
		if _, ok := want[e]; ok {
			want[e] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("dropEnv missing %q (got %v)", k, env)
		}
	}
	// PATH must be present and non-empty (the child gets a sane default).
	hasPath := false
	for _, e := range env {
		if len(e) > 5 && e[:5] == "PATH=" {
			hasPath = true
		}
	}
	if !hasPath {
		t.Errorf("dropEnv missing PATH (got %v)", env)
	}
}

// The no-console-user skip path at the runner layer: a system-mode runner
// dispatching a current_user plugin with no console user records an
// UNAVAILABLE run (queryable) and never execs the plugin.
func TestRunOnceNoConsoleUserRecordsUnavailable(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	r := NewWithMode(db, osadapter.ModeSystem)
	r.consoleUser = fakeConsoleUser(0, 0, "", "", nil) // nobody logged in

	// A plugin that, if it DID run, would create a sentinel file. We assert
	// it never runs.
	sentinel := filepath.Join(t.TempDir(), "ran")
	p := testutil.ScriptPlugin(t, "skill-protector",
		"touch '"+sentinel+"'\n"+`echo '{"status":"ok"}'`)
	p.Manifest.RunAs = plugin.RunAsCurrentUser

	out, err := r.Run(context.Background(), Job{ID: "j1"}, p, "scheduler")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Status != state.RunStatusUnavailable {
		t.Fatalf("status = %q, want unavailable", out.Status)
	}
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatal("plugin executed as root despite no console user — corruption risk")
	}
	last, err := db.Runs.LastByStatus("j1", state.RunStatusUnavailable)
	if err != nil {
		t.Fatalf("unavailable run not persisted: %v", err)
	}
	if last.Status != state.RunStatusUnavailable {
		t.Errorf("persisted status = %q", last.Status)
	}
}
