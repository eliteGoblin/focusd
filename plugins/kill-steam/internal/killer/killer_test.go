package killer

import (
	"errors"
	"testing"
)

func newFake(procs []procView, killErr map[int]error) *Killer {
	k := New(nil)
	k.list = func() ([]procView, error) { return procs, nil }
	k.killPID = func(pid int) error { return killErr[pid] }
	return k
}

func TestKillsExactMatchOnly(t *testing.T) {
	// "msteams" must survive: it contains "steam" but is not an exact
	// match (the v0.6.1 #17 regression guard).
	procs := []procView{
		{PID: 10, Name: "Steam"},
		{PID: 11, Name: "steam_osx"},
		{PID: 12, Name: "msteams"},     // MUST NOT be killed
		{PID: 13, Name: "MSTeams"},     // MUST NOT be killed
		{PID: 14, Name: "Slack"},       // unrelated
		{PID: 15, Name: "dota2"},       // killed
		{PID: 16, Name: "STEAM"},       // case-insensitive exact -> killed
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
	k.list = func() ([]procView, error) {
		return []procView{{PID: 1, Name: "Steam"}, {PID: 2, Name: "OnlyThis"}}, nil
	}
	k.killPID = func(int) error { return nil }
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
