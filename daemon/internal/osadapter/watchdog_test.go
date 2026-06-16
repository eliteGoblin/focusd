//go:build darwin

package osadapter

import (
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// fakeCron is an in-memory cronTab seam. content models the on-disk crontab;
// replaces counts how many times the whole table was rewritten so a test can
// assert idempotency (a no-op second call must NOT rewrite).
type fakeCron struct {
	content  string
	replaces int
	listErr  error
}

func (c *fakeCron) list() (string, error) {
	if c.listErr != nil {
		return "", c.listErr
	}
	return c.content, nil
}
func (c *fakeCron) replace(s string) error {
	c.content = s
	c.replaces++
	return nil
}

// fakeCopyFS is an in-memory copyFS: it records placements and hands back a
// deterministic disguised path under the requested dir so tests can assert the
// resulting cron line without a real filesystem.
type fakeCopyFS struct {
	placed   []string // dirs a copy was placed into
	removed  []string // dirs removed
	nextName string   // basename returned by relocateInto
}

func (f *fakeCopyFS) relocateInto(_, dir string) (string, error) {
	f.placed = append(f.placed, dir)
	name := f.nextName
	if name == "" {
		name = "com.apple.metadata.helper.deadbeef"
	}
	return dir + "/" + name, nil
}
func (f *fakeCopyFS) removeAll(dir string) error {
	f.removed = append(f.removed, dir)
	return nil
}

func fakeSelf() (string, error) { return "/cur/daemon-bin", nil }

const wdDesired = "v1.2.3"

// TestEnsureWatchdogReadModifyWrite covers acceptance #3 (a single install
// stands up the cron rail) and the idempotency requirement: absent → line
// written; present → second call is a no-op (no dup line, no rewrite).
func TestEnsureWatchdogReadModifyWrite(t *testing.T) {
	cases := []struct {
		name         string
		start        string
		copyPath     string
		wantContains string
		wantReplaces int
		wantPlaced   bool
	}{
		{
			name:         "absent writes line with explicit copy path",
			start:        "",
			copyPath:     "/sep/wd/bin",
			wantContains: "* * * * * /sep/wd/bin watchdog -v " + wdDesired + " >/dev/null 2>&1",
			wantReplaces: 1,
		},
		{
			name:         "absent with empty copyPath places a fresh copy",
			start:        "",
			copyPath:     "",
			wantContains: " watchdog -v " + wdDesired,
			wantReplaces: 1,
			wantPlaced:   true,
		},
		{
			name:         "preserves unrelated existing cron lines",
			start:        "0 9 * * * /usr/bin/backup\n",
			copyPath:     "/sep/wd/bin",
			wantContains: "/usr/bin/backup",
			wantReplaces: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct := &fakeCron{content: tc.start}
			fs := &fakeCopyFS{}
			if err := ensureWatchdog(mode.User, tc.copyPath, wdDesired, ct, fs, fakeSelf); err != nil {
				t.Fatalf("ensureWatchdog: %v", err)
			}
			if ct.replaces != tc.wantReplaces {
				t.Fatalf("replaces = %d, want %d", ct.replaces, tc.wantReplaces)
			}
			if !strings.Contains(ct.content, tc.wantContains) {
				t.Fatalf("crontab %q missing %q", ct.content, tc.wantContains)
			}
			if tc.wantPlaced && len(fs.placed) == 0 {
				t.Fatalf("expected a copy to be placed, none was")
			}
			if !tc.wantPlaced && len(fs.placed) != 0 {
				t.Fatalf("did not expect a copy placement, got %v", fs.placed)
			}
		})
	}
}

// TestEnsureWatchdogIdempotent: a second EnsureWatchdog on an already-wired
// crontab must NOT rewrite (no duplicate line) — the read-modify-write
// idempotency guarantee on reinstall.
func TestEnsureWatchdogIdempotent(t *testing.T) {
	ct := &fakeCron{}
	fs := &fakeCopyFS{}
	if err := ensureWatchdog(mode.User, "/sep/wd/bin", wdDesired, ct, fs, fakeSelf); err != nil {
		t.Fatalf("first: %v", err)
	}
	first := ct.content
	if err := ensureWatchdog(mode.User, "/sep/wd/bin", wdDesired, ct, fs, fakeSelf); err != nil {
		t.Fatalf("second: %v", err)
	}
	if ct.replaces != 1 {
		t.Fatalf("second call rewrote crontab: replaces = %d, want 1", ct.replaces)
	}
	if ct.content != first {
		t.Fatalf("crontab changed on idempotent call:\n%q\n%q", first, ct.content)
	}
	if strings.Count(ct.content, cronMarker) != 1 {
		t.Fatalf("duplicate watchdog line: %q", ct.content)
	}
}

// TestRemoveWatchdog strips our line + removes the copy dir, leaving unrelated
// lines intact (best-effort teardown).
func TestRemoveWatchdog(t *testing.T) {
	ct := &fakeCron{
		content: "0 9 * * * /usr/bin/backup\n" +
			"* * * * * /sep/wd/bin watchdog -v " + wdDesired + " >/dev/null 2>&1\n",
	}
	fs := &fakeCopyFS{}
	if err := removeWatchdog(mode.User, ct, fs); err != nil {
		t.Fatalf("removeWatchdog: %v", err)
	}
	if strings.Contains(ct.content, cronMarker) {
		t.Fatalf("watchdog line not stripped: %q", ct.content)
	}
	if !strings.Contains(ct.content, "/usr/bin/backup") {
		t.Fatalf("unrelated line lost: %q", ct.content)
	}
	if len(fs.removed) != 1 || fs.removed[0] != "/sep/wd" {
		t.Fatalf("copy dir not removed (parent of copy path): %v", fs.removed)
	}
}

// TestRemoveWatchdogAbsentNoRewrite: removing when no line is present must not
// rewrite the crontab (best-effort, idempotent).
func TestRemoveWatchdogAbsentNoRewrite(t *testing.T) {
	ct := &fakeCron{content: "0 9 * * * /usr/bin/backup\n"}
	fs := &fakeCopyFS{}
	if err := removeWatchdog(mode.User, ct, fs); err != nil {
		t.Fatalf("removeWatchdog: %v", err)
	}
	if ct.replaces != 0 {
		t.Fatalf("rewrote crontab with no watchdog line present: replaces = %d", ct.replaces)
	}
}

// TestRefreshWatchdogRewritesInPlace: a refresh after self-update places a
// fresh copy and rewrites the line to the new copy path — exactly ONE marker
// line remains (no orphaned stale duplicate in the crontab).
func TestRefreshWatchdogRewritesInPlace(t *testing.T) {
	ct := &fakeCron{
		content: "* * * * * /old/wd/bin watchdog -v v1.0.0 >/dev/null 2>&1\n",
	}
	fs := &fakeCopyFS{nextName: "new-copy"}
	if err := refreshWatchdog(mode.User, "/new/daemon-bin", wdDesired, ct, fs); err != nil {
		t.Fatalf("refreshWatchdog: %v", err)
	}
	if strings.Count(ct.content, cronMarker) != 1 {
		t.Fatalf("expected exactly one watchdog line, got: %q", ct.content)
	}
	if strings.Contains(ct.content, "/old/wd/bin") {
		t.Fatalf("old copy path still in crontab: %q", ct.content)
	}
	if !strings.Contains(ct.content, "new-copy watchdog -v "+wdDesired) {
		t.Fatalf("new copy line not present: %q", ct.content)
	}
	if len(fs.placed) != 1 {
		t.Fatalf("expected one fresh copy placement, got %v", fs.placed)
	}
}

// TestCronLineCopyPathRecovery: the copy path is recovered by parsing the
// crontab line — the single source of truth (never stored in the wiped
// workdir).
func TestCronLineCopyPathRecovery(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no crontab", "", ""},
		{"unrelated only", "0 9 * * * /usr/bin/backup\n", ""},
		{
			"our line",
			"* * * * * /sep/hidden/bin watchdog -v v2.0.0 >/dev/null 2>&1\n",
			"/sep/hidden/bin",
		},
		{
			"our line among others",
			"0 9 * * * /usr/bin/backup\n* * * * * /x/y/z watchdog -v v1.0.0 >/dev/null 2>&1\n",
			"/x/y/z",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cronLineCopyPath(tc.in); got != tc.want {
				t.Fatalf("cronLineCopyPath = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWatchdogStatusBools: status reports cronPresent/copyOK as bools only.
// With a real (absent) copy path the copy is not OK; the cron line is present.
func TestWatchdogStatusBools(t *testing.T) {
	ct := &fakeCron{
		content: "* * * * * /no/such/copy watchdog -v " + wdDesired + " >/dev/null 2>&1\n",
	}
	cronPresent, copyOK := watchdogStatus(ct)
	if !cronPresent {
		t.Fatalf("cronPresent = false, want true")
	}
	if copyOK {
		t.Fatalf("copyOK = true for a nonexistent copy path, want false")
	}

	empty := &fakeCron{}
	if cp, ok := watchdogStatus(empty); cp || ok {
		t.Fatalf("empty crontab: got (%v,%v), want (false,false)", cp, ok)
	}
}
