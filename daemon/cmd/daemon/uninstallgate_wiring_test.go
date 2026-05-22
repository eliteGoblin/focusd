package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/uninstallgate"
)

// withStdin swaps os.Stdin for the duration of fn (empty input here).
func withEmptyStdin(t *testing.T, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.Close() // immediate EOF
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old; r.Close() }()
	fn()
}

func TestRunUninstallGate_FreshDoesNotProceed(t *testing.T) {
	gpath := filepath.Join(t.TempDir(), "gate")
	withEmptyStdin(t, func() {
		code, proceed := runUninstallGate(gpath)
		if proceed {
			t.Fatal("a fresh gate must never proceed to teardown")
		}
		if code != 1 {
			t.Fatalf("empty/instant input must be rejected (code 1), got %d", code)
		}
	})
	// No progress recorded (rejected input must not advance).
	if uninstallgate.Load(gpath, time.Now()).Step != 0 {
		t.Fatal("rejected transcription must not advance the gate")
	}
}

func TestRunUninstallGate_WaitDoesNotProceed(t *testing.T) {
	gpath := filepath.Join(t.TempDir(), "gate")
	now := time.Now()
	// Step 1 done just now → inside the 2h cool-off.
	if err := uninstallgate.Save(gpath, uninstallgate.State{Step: 1, T1: now, LastSeen: now}); err != nil {
		t.Fatal(err)
	}
	code, proceed := runUninstallGate(gpath)
	if proceed || code != 1 {
		t.Fatalf("during cool-off must wait (1,false), got (%d,%v)", code, proceed)
	}
}

func TestRunUninstallGate_CompleteProceeds(t *testing.T) {
	gpath := filepath.Join(t.TempDir(), "gate")
	past := time.Now().Add(-time.Hour)
	// A genuinely completed state has both step timestamps set (Evaluate
	// rejects "Step done without timestamp" as a crafted bypass).
	if err := uninstallgate.Save(gpath, uninstallgate.State{
		Step: uninstallgate.TotalSteps, T1: past, T2: past, LastSeen: past,
	}); err != nil {
		t.Fatal(err)
	}
	code, proceed := runUninstallGate(gpath)
	if !proceed || code != 0 {
		t.Fatalf("completed gate must proceed (0,true), got (%d,%v)", code, proceed)
	}
}
