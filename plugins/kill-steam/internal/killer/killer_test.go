package killer

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// newFake wires a stateful, instant Killer: a successful kill removes the
// PID from the process list (so the post-kill re-scan reflects reality),
// and the settle delay is zeroed so tests never sleep.
func newFake(procs []procView, killErr map[int]error) *Killer {
	k := New(nil)
	k.settle = 0
	k.sleep = func(time.Duration) {}
	killed := map[int]bool{}
	k.list = func() ([]procView, error) {
		live := make([]procView, 0, len(procs))
		for _, p := range procs {
			if killed[p.PID] {
				continue
			}
			live = append(live, p)
		}
		return live, nil
	}
	k.killPID = func(pid int) error {
		if err := killErr[pid]; err != nil {
			return err
		}
		killed[pid] = true
		return nil
	}
	return k
}

func TestKillsExactMatchOnly(t *testing.T) {
	// "msteams" must survive: it contains "steam" but is not an exact
	// match (the v0.6.1 #17 regression guard).
	procs := []procView{
		{PID: 10, Name: "Steam"},
		{PID: 11, Name: "steam_osx"},
		{PID: 12, Name: "msteams"}, // MUST NOT be killed
		{PID: 13, Name: "MSTeams"}, // MUST NOT be killed
		{PID: 14, Name: "Slack"},   // unrelated
		{PID: 15, Name: "dota2"},   // killed
		{PID: 16, Name: "STEAM"},   // case-insensitive exact -> killed
	}
	out, err := newFake(procs, nil).Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := map[int]bool{10: true, 11: true, 15: true, 16: true}
	if out.KilledCount() != len(want) {
		t.Fatalf("killed %v, want pids %v", out.KilledPIDs, want)
	}
	for _, pid := range out.KilledPIDs {
		if !want[pid] {
			t.Errorf("killed unexpected pid %d", pid)
		}
	}
	if out.Scanned != len(procs) {
		t.Errorf("scanned = %d, want %d", out.Scanned, len(procs))
	}
}

func TestNothingRunningIsCleanOutcome(t *testing.T) {
	out, err := newFake([]procView{{PID: 1, Name: "Finder"}}, nil).Run()
	if err != nil || out.KilledCount() != 0 || len(out.Failed) != 0 {
		t.Fatalf("expected clean empty outcome, got %+v err=%v", out, err)
	}
}

func TestKillFailureRecordedNotFatal(t *testing.T) {
	procs := []procView{{PID: 10, Name: "Steam"}, {PID: 11, Name: "dota2"}}
	out, err := newFake(procs, map[int]error{10: errors.New("EPERM")}).Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.KilledCount() != 1 || out.KilledPIDs[0] != 11 {
		t.Errorf("expected only pid 11 killed, got %v", out.KilledPIDs)
	}
	if len(out.Failed) != 1 {
		t.Errorf("expected 1 failure recorded, got %v", out.Failed)
	}
}

func TestCustomNamesOverrideDefaults(t *testing.T) {
	k := New([]string{"OnlyThis"})
	k.settle = 0
	k.sleep = func(time.Duration) {}
	killed := map[int]bool{}
	all := []procView{{PID: 1, Name: "Steam"}, {PID: 2, Name: "OnlyThis"}}
	k.list = func() ([]procView, error) {
		var live []procView
		for _, p := range all {
			if !killed[p.PID] {
				live = append(live, p)
			}
		}
		return live, nil
	}
	k.killPID = func(pid int) error { killed[pid] = true; return nil }
	out, _ := k.Run()
	if out.KilledCount() != 1 || out.KilledPIDs[0] != 2 {
		t.Errorf("custom names not honored: %+v", out)
	}
}

func TestListErrorPropagates(t *testing.T) {
	k := New(nil)
	k.list = func() ([]procView, error) { return nil, errors.New("boom") }
	if _, err := k.Run(); err == nil {
		t.Error("expected error when process enumeration fails")
	}
}

func TestListProcessesRealIsSafe(t *testing.T) {
	// Enumeration does not kill anything; the test process itself must
	// appear, so the slice is non-empty.
	procs, err := listProcesses()
	if err != nil {
		t.Fatalf("listProcesses: %v", err)
	}
	if len(procs) == 0 {
		t.Error("expected at least this test process")
	}
}

func TestKillProcessInvalidPID(t *testing.T) {
	// PID 0x7fffffff will not exist; must error, never panic, never
	// kill anything real.
	if err := killProcess(0x7fffffff); err == nil {
		t.Error("expected error killing non-existent pid")
	}
}

func TestNewDefaultWiring(t *testing.T) {
	k := New(nil)
	if k.list == nil || k.killPID == nil {
		t.Fatal("New must wire real list/kill seams")
	}
	if len(k.names) != len(DefaultProcessNames) {
		t.Errorf("New(nil) should use defaults")
	}
}

func TestRealRunKillsNothingForUnknownName(t *testing.T) {
	// Exercises the real enumeration path end-to-end with a name that
	// cannot match any process, so nothing is killed.
	out, err := New([]string{"zzz-focusd-test-nonexistent-proc"}).Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.KilledCount() != 0 || len(out.Failed) != 0 {
		t.Errorf("expected zero kills, got %+v", out)
	}
	if out.Scanned == 0 {
		t.Error("expected to have scanned real processes")
	}
}

func TestDefaultsCoverSteamAndDota(t *testing.T) {
	got := map[string]bool{}
	for _, n := range DefaultProcessNames {
		got[n] = true
	}
	for _, must := range []string{"Steam", "steamwebhelper", "dota2", "Dota 2"} {
		if !got[must] {
			t.Errorf("default process names missing %q", must)
		}
	}
}

// TestMatchesByPathUnderSteamBundle is the core false-green regression:
// a generically-named helper (ipcserver) that a name-only match misses,
// but which executes from under the Steam bundle, MUST be killed — while
// an unrelated binary that merely shares that generic name (and is NOT
// under a Steam path) MUST be left alone. This is the live-observed
// survivor: /…/Library/Application Support/Steam/Steam.AppBundle/…/ipcserver.
func TestMatchesByPathUnderSteamBundle(t *testing.T) {
	const appSupport = "/Users/testuser/Library/Application Support/Steam"
	procs := []procView{
		// generic comm name, but under the Steam bundle → MUST be killed
		{PID: 20, Name: "ipcserver", Path: appSupport + "/Steam.AppBundle/Steam/Contents/MacOS/ipcserver"},
		// another generic helper under Application Support/Steam → killed
		{PID: 21, Name: "gameoverlayui", Path: appSupport + "/gameoverlayui"},
		// Steam.app bundle path (case varies) → killed by path
		{PID: 22, Name: "someHelper", Path: "/Applications/Steam.app/Contents/MacOS/steam_osx"},
		// SAME generic name but NOT under any Steam path → MUST survive
		{PID: 23, Name: "ipcserver", Path: "/usr/local/bin/ipcserver"},
		// unrelated app → survives
		{PID: 24, Name: "Finder", Path: "/System/Library/CoreServices/Finder.app/Contents/MacOS/Finder"},
		// classic name matches still work (name-only, empty path)
		{PID: 25, Name: "Steam", Path: ""},
		{PID: 26, Name: "dota2", Path: ""},
	}
	out, err := newFake(procs, nil).Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := map[int]bool{20: true, 21: true, 22: true, 25: true, 26: true}
	if out.KilledCount() != len(want) {
		t.Fatalf("killed %v, want %v", out.KilledPIDs, want)
	}
	for _, pid := range out.KilledPIDs {
		if !want[pid] {
			t.Errorf("killed unexpected pid %d (path/name over-match)", pid)
		}
	}
	// The unrelated /usr/local/bin/ipcserver must never be touched.
	for _, pid := range out.KilledPIDs {
		if pid == 23 {
			t.Fatal("false-matched unrelated /usr/local/bin/ipcserver by generic name")
		}
	}
	if len(out.Survivors) != 0 {
		t.Errorf("expected no survivors after successful kills, got %v", out.Survivors)
	}
}

// TestSurvivorAfterKillIsNotClean locks the honest-verdict re-scan: when a
// Steam-bundle process is STILL present after the kill (kill didn't take /
// relaunched), it must be reported as a survivor so the verdict cannot read
// as clean "ok" over a live Steam.
func TestSurvivorAfterKillIsNotClean(t *testing.T) {
	steam := procView{
		PID:  30,
		Name: "ipcserver",
		Path: "/Users/testuser/Library/Application Support/Steam/Steam.AppBundle/Steam/Contents/MacOS/ipcserver",
	}
	k := New(nil)
	k.settle = 0
	k.sleep = func(time.Duration) {}
	// The process persists across both scans (relaunch / kill ineffective).
	k.list = func() ([]procView, error) { return []procView{steam}, nil }
	k.killPID = func(int) error { return nil } // "succeeds" but proc survives
	out, err := k.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.KilledCount() != 1 {
		t.Fatalf("expected the matched pid to be killed once, got %v", out.KilledPIDs)
	}
	if len(out.Survivors) != 1 || out.Survivors[0] != 30 {
		t.Fatalf("expected survivor pid 30, got %v", out.Survivors)
	}
}

// TestRescanErrorIsSurfacedNotSwallowed is the CRITICAL guard: if the
// post-kill re-scan itself fails, that failure MUST be surfaced (so the
// caller cannot report a clean "ok" over an unverified pass). The original
// design silently discarded the rescan error — reintroducing the exact
// false-green class this fix closes.
func TestRescanErrorIsSurfacedNotSwallowed(t *testing.T) {
	k := New(nil)
	k.settle = 0
	k.sleep = func(time.Duration) {}
	calls := 0
	k.list = func() ([]procView, error) {
		calls++
		if calls == 1 {
			return []procView{{PID: 40, Name: "Steam"}}, nil // initial scan matches
		}
		return nil, errors.New("proc enum boom") // re-scan cannot run
	}
	k.killPID = func(int) error { return nil }
	out, err := k.Run()
	if err != nil {
		t.Fatalf("Run must not hard-error on rescan failure: %v", err)
	}
	if out.RescanError == "" {
		t.Fatal("rescan failure must be surfaced (RescanError), not swallowed into a clean ok")
	}
}

// TestSurvivorClearsWithinPollWindow: a process still present on the first
// re-scan but gone on a later poll is NOT a survivor (the kill took, it just
// hadn't been reaped yet) — guards against a spurious status=failed.
func TestSurvivorClearsWithinPollWindow(t *testing.T) {
	k := New(nil)
	k.settle = 0
	k.sleep = func(time.Duration) {}
	scan := 0
	k.list = func() ([]procView, error) {
		scan++
		if scan <= 2 { // initial scan + first re-scan still show Steam
			return []procView{{PID: 50, Name: "Steam"}}, nil
		}
		return nil, nil // reaped by the second re-scan
	}
	k.killPID = func(int) error { return nil }
	out, err := k.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Survivors) != 0 {
		t.Fatalf("process reaped within the poll window must not be a survivor, got %v", out.Survivors)
	}
	if out.RescanError != "" {
		t.Fatalf("unexpected rescan error: %s", out.RescanError)
	}
}

// TestFailedKillNotDoubleCountedAsSurvivor: a PID whose kill errored is
// recorded in Failed and MUST NOT also appear in Survivors (disjoint lists).
func TestFailedKillNotDoubleCountedAsSurvivor(t *testing.T) {
	// Static list: the failed-kill Steam persists across re-scans.
	k := New(nil)
	k.settle = 0
	k.sleep = func(time.Duration) {}
	k.list = func() ([]procView, error) { return []procView{{PID: 60, Name: "Steam"}}, nil }
	k.killPID = func(int) error { return errors.New("EPERM") }
	out, err := k.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Failed) != 1 {
		t.Fatalf("expected pid 60 in Failed, got %v", out.Failed)
	}
	if len(out.Survivors) != 0 {
		t.Fatalf("failed-kill PID must not double-count as a survivor, got %v", out.Survivors)
	}
}

// TestNoSurvivorRescanWhenNothingMatched: the settle+re-scan only runs when
// something matched, so a steady-state (no Steam) pass stays cheap and never
// sleeps. The sleep seam panics to prove it is not called.
func TestNoSurvivorRescanWhenNothingMatched(t *testing.T) {
	k := New(nil)
	k.sleep = func(time.Duration) { t.Fatal("must not sleep/re-scan when nothing matched") }
	k.list = func() ([]procView, error) {
		return []procView{{PID: 1, Name: "Finder", Path: "/System/.../Finder"}}, nil
	}
	k.killPID = func(int) error { return nil }
	out, err := k.Run()
	if err != nil || out.KilledCount() != 0 || len(out.Survivors) != 0 {
		t.Fatalf("expected clean no-match outcome, got %+v err=%v", out, err)
	}
}

func TestPathMatchIsCaseInsensitive(t *testing.T) {
	k := New(nil)
	want := lowerSet(k.names)
	upper := procView{PID: 9, Name: "x", Path: "/USERS/X/LIBRARY/APPLICATION SUPPORT/STEAM/STEAM.APPBUNDLE/X"}
	if !k.matches(upper, want) {
		t.Error("Steam bundle path match must be case-insensitive")
	}
}

// TestMatchesEmptyNamePathOnly documents the enumeration fix: a helper whose
// comm name is unreadable (empty) but which resolves to a Steam bundle path
// MUST still match by path. This is why listProcesses keeps a PID when Name()
// errors as long as Exe() is readable — a path-only survivor is not lost.
func TestMatchesEmptyNamePathOnly(t *testing.T) {
	k := New(nil)
	want := lowerSet(k.names)
	p := procView{PID: 7, Name: "", Path: "/Users/testuser/Library/Application Support/Steam/Steam.AppBundle/x"}
	if !k.matches(p, want) {
		t.Error("empty-name process under a Steam path must match by path")
	}
}

// --- #111: single-spawn enumeration (perf regression guards) ---
//
// #110 matched Steam helpers by executable path but read that path with a
// per-PID gopsutil Exe() call (one proc_pidpath syscall per process). Under
// the platform's per-job timeout + machine load the O(N) syscalls blew the
// budget → kill-steam-reconcile flipped DEGRADED. The fix reads every
// process's pid + full exe path from a SINGLE `ps` spawn and parses it. These
// tests lock the fast path and its preserved match behavior, and would fail
// if per-PID enumeration were reintroduced.

// TestParsePSExtractsPidNameAndFullPath verifies the pure parser: each line is
// "<pid> <full-exe-path>", the exe path may contain spaces (split on the FIRST
// space only), Name is the path basename, Path is the full exe path.
func TestParsePSExtractsPidNameAndFullPath(t *testing.T) {
	const appSupport = "/Users/testuser/Library/Application Support/Steam"
	raw := []byte(strings.Join([]string{
		"    1 /sbin/launchd",
		// space-containing path: basename must stay intact, path preserved whole
		"  796 /System/Library/CoreServices/Finder.app/Contents/MacOS/Finder",
		"  900 " + appSupport + "/Steam.AppBundle/Steam/Contents/MacOS/ipcserver",
		"  901 /usr/local/bin/ipcserver",
		"  902 /Applications/Steam.app/Contents/MacOS/steam_osx",
		"  903 /Applications/Dota 2 beta/game/dota.app/Contents/MacOS/dota2",
		"", // trailing blank line tolerated
	}, "\n"))

	byPID := map[int]procView{}
	for _, p := range parsePS(raw) {
		byPID[p.PID] = p
	}
	if len(byPID) != 6 {
		t.Fatalf("parsed %d procs, want 6: %+v", len(byPID), byPID)
	}
	if p := byPID[900]; p.Name != "ipcserver" || !strings.HasSuffix(p.Path, "/Contents/MacOS/ipcserver") || !strings.Contains(p.Path, "/Steam.AppBundle/") {
		t.Errorf("pid 900 (bundle ipcserver) parsed wrong: %+v", p)
	}
	if p := byPID[903]; p.Name != "dota2" || !strings.Contains(p.Path, "/Dota 2 beta/") {
		t.Errorf("space-containing exe path not preserved for pid 903: %+v", p)
	}
	if p := byPID[796]; p.Name != "Finder" {
		t.Errorf("basename of pid 796 = %q, want Finder", p.Name)
	}
}

// TestParsedListDrivesKillMatch runs the full ps→parse→match pipeline through
// an injected list and asserts #110 behavior is preserved end-to-end: an
// ipcserver under the Steam bundle IS killed, an unrelated same-named
// /usr/local/bin/ipcserver is NOT, and main Steam/Dota2 are killed by name.
func TestParsedListDrivesKillMatch(t *testing.T) {
	const appSupport = "/Users/testuser/Library/Application Support/Steam"
	raw := []byte(strings.Join([]string{
		"    1 /sbin/launchd",
		"  796 /System/Library/CoreServices/Finder.app/Contents/MacOS/Finder",
		"  900 " + appSupport + "/Steam.AppBundle/Steam/Contents/MacOS/ipcserver",
		"  901 /usr/local/bin/ipcserver",
		"  902 /Applications/Steam.app/Contents/MacOS/steam_osx",
		"  903 /Applications/Dota 2 beta/game/dota.app/Contents/MacOS/dota2",
	}, "\n"))

	k := New(nil)
	k.settle = 0
	k.sleep = func(time.Duration) {}
	killed := map[int]bool{}
	k.list = func() ([]procView, error) {
		live := make([]procView, 0)
		for _, p := range parsePS(raw) {
			if !killed[p.PID] {
				live = append(live, p)
			}
		}
		return live, nil
	}
	k.killPID = func(pid int) error { killed[pid] = true; return nil }

	out, err := k.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := map[int]bool{900: true, 902: true, 903: true} // bundle ipcserver + steam_osx + dota2
	if out.KilledCount() != len(want) {
		t.Fatalf("killed %v, want pids %v", out.KilledPIDs, want)
	}
	for _, pid := range out.KilledPIDs {
		if !want[pid] {
			t.Errorf("killed unexpected pid %d (over-match)", pid)
		}
		if pid == 901 {
			t.Fatal("false-matched unrelated /usr/local/bin/ipcserver")
		}
	}
	if len(out.Survivors) != 0 {
		t.Errorf("expected no survivors, got %v", out.Survivors)
	}
}

// TestEnumerationIsSingleSpawnNotPerPID is the core perf-regression guard: the
// enumeration must issue exactly ONE process listing regardless of process
// count. A reintroduction of per-PID enumeration (one proc_pidpath syscall per
// process — the #110→#111 regression) would scale spawns with N; this locks it
// to O(1).
func TestEnumerationIsSingleSpawnNotPerPID(t *testing.T) {
	var b strings.Builder
	const n = 500
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%d /usr/bin/proc%d\n", i, i)
	}
	orig := psCommand
	t.Cleanup(func() { psCommand = orig })
	calls := 0
	psCommand = func() ([]byte, error) { calls++; return []byte(b.String()), nil }

	procs, err := listProcesses()
	if err != nil {
		t.Fatalf("listProcesses: %v", err)
	}
	if calls != 1 {
		t.Fatalf("enumeration spawned %d times for %d processes; must be exactly 1 (O(1) spawns)", calls, n)
	}
	if len(procs) != n {
		t.Fatalf("parsed %d processes, want %d", len(procs), n)
	}
}

// TestListProcessesSurfacesPSError: a failing ps spawn must propagate, not be
// swallowed into a silent empty (and thus falsely-clean) process list.
func TestListProcessesSurfacesPSError(t *testing.T) {
	orig := psCommand
	t.Cleanup(func() { psCommand = orig })
	psCommand = func() ([]byte, error) { return nil, errors.New("ps boom") }
	if _, err := listProcesses(); err == nil {
		t.Fatal("expected ps spawn error to surface")
	}
}

func TestDefaultSteamPathMarkersPresent(t *testing.T) {
	got := map[string]bool{}
	for _, m := range DefaultSteamPathMarkers {
		got[m] = true
	}
	for _, must := range []string{"/steam.appbundle/", "/library/application support/steam/"} {
		if !got[must] {
			t.Errorf("default steam path markers missing %q", must)
		}
	}
}
