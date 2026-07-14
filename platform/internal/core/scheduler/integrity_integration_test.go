package scheduler

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/platform/internal/core/config"
	"github.com/eliteGoblin/focusd/platform/internal/core/plugin"
	"github.com/eliteGoblin/focusd/platform/internal/core/runner"
	"github.com/eliteGoblin/focusd/platform/internal/core/state"
	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
)

// restoringVerifier is a faithful test double of bundle.VerifyOrRestore +
// bundle.IsBundled: the embedded genuine bytes are the trust root, and any
// on-disk drift is atomically restored to them. It lets this integration test
// exercise the real scheduler → runner → verify → restore → exec → event path
// against a SAFE built stub, instead of executing a genuine bundled binary
// (which is privilege-gated or side-effecting — e.g. kill-steam does an on-disk
// uninstall). The contract is identical to the production bundle impl.
type restoringVerifier struct {
	genuine []byte // the golden binary content
	binName string // the plugin binary's filename inside its subdir
}

func (v *restoringVerifier) IsBundled(subdir string) bool { return subdir == v.binName }

func (v *restoringVerifier) VerifyOrRestore(pluginRoot, subdir string) (bool, string, string, error) {
	binPath := filepath.Join(pluginRoot, subdir, v.binName)
	cur, err := os.ReadFile(binPath)
	if err == nil && bytes.Equal(cur, v.genuine) {
		return false, "", "", nil // genuine already on disk
	}
	// Mismatch (or missing): atomically restore the golden content.
	tmp := binPath + ".tmp"
	if werr := os.WriteFile(tmp, v.genuine, 0o755); werr != nil {
		return false, "", "", werr
	}
	if rerr := os.Rename(tmp, binPath); rerr != nil {
		return false, "", "", rerr
	}
	return true, "genuine00abc1", "tampered9def2", nil
}

// TestSchedulerRestoresTamperedPluginBetweenTicks is the FEATURE 23 integration
// test: a plugin is overwritten with an `exit 0` do-nothing stub between two
// scheduler ticks. The NEXT tick must (a) restore + execute the GENUINE binary,
// and (b) record a tamper-repaired security event — never run the substitute.
func TestSchedulerRestoresTamperedPluginBetweenTicks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("integration stub uses a POSIX shell plugin")
	}
	tmp := t.TempDir()
	pluginRoot := filepath.Join(tmp, "plugins")
	const sub = "guarded-job"
	dir := filepath.Join(pluginRoot, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, sub)

	// GENUINE binary: prints a distinctive marker so a run can be attributed to
	// the genuine program vs. the tamper substitute.
	genuine := []byte("#!/bin/sh\necho '{\"status\":\"ok\",\"message\":\"GENUINE-RAN\"}'\nexit 0\n")
	if err := os.WriteFile(binPath, genuine, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"guarded-job","name":"Guarded","version":"1.0.0","type":"job",
"protocol_version":"1","entrypoint":"./guarded-job",
"supported_os":["` + runtime.GOOS + `"],"supported_arch":["` + runtime.GOARCH + `"],
"required_privilege":"user","run_as":"current_user"}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	v := &restoringVerifier{genuine: append([]byte(nil), genuine...), binName: sub}

	db, err := state.Open(filepath.Join(tmp, "state.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Real discovery, wired with the authenticity guard (FEATURE 23 Fix 1/3).
	disc := (&plugin.Discoverer{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Mode: osadapter.ModeUser}).
		WithIntegrity(v)
	found, err := disc.Discover(pluginRoot)
	if err != nil || len(found) != 1 || !found[0].OK {
		t.Fatalf("discovery failed: %v %+v", err, found)
	}
	byID := map[string]plugin.Discovered{found[0].Manifest.ID: found[0]}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	run := runner.NewWithMode(db, osadapter.ModeUser).WithVerifier(v).WithLogger(log)
	s := New(run, db, log, osadapter.ModeUser)

	job := config.Job{ID: "guarded", Plugin: "guarded-job", Enabled: true,
		Schedule: "@every 1h", Timeout: config.Duration(10 * time.Second)}
	n, err := s.Register([]config.Job{job}, byID)
	if err != nil || n != 1 {
		t.Fatalf("Register: n=%d err=%v", n, err)
	}

	// --- Tick 1: genuine binary runs cleanly. ---
	s.trigger(job, found[0])
	assertLastRunIsGenuine(t, db, "guarded", "tick 1 (clean)")

	// --- Tamper: overwrite the plugin with an exit-0 do-nothing stub that
	// would produce a DIFFERENT (non-genuine) result if it ran. ---
	stub := []byte("#!/bin/sh\necho '{\"status\":\"ok\",\"message\":\"TAMPER-SUBSTITUTE-RAN\"}'\nexit 0\n")
	if err := os.WriteFile(binPath, stub, 0o755); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	// --- Tick 2: the point-of-use check must restore + run the GENUINE binary
	// and record a tamper event — the substitute must never execute. ---
	s.trigger(job, found[0])
	assertLastRunIsGenuine(t, db, "guarded", "tick 2 (after tamper)")

	// The on-disk binary must be back to genuine content.
	if got, _ := os.ReadFile(binPath); !bytes.Equal(got, genuine) {
		t.Errorf("plugin binary not restored to genuine content after tick 2")
	}

	// A tamper-repaired event must have been recorded.
	ev, _ := db.Events.Recent(20)
	tamper := 0
	for _, e := range ev {
		if e.EventType == state.EventTamperRepaired {
			tamper++
		}
	}
	if tamper == 0 {
		t.Error("expected a plugin_tamper_repaired event after the between-ticks swap")
	}
	// The substitute's marker must appear in NO run row (it never executed).
	hist, _ := db.Runs.History("guarded", 20)
	for _, h := range hist {
		if strings.Contains(h.StdoutJSON, "TAMPER-SUBSTITUTE-RAN") {
			t.Fatalf("substitute binary executed — its output leaked into a run row: %q", h.StdoutJSON)
		}
	}
	t.Logf("integration OK: %d tamper-repaired event(s); every run executed the GENUINE binary; substitute never ran", tamper)
}

func assertLastRunIsGenuine(t *testing.T, db *state.DB, jobID, phase string) {
	t.Helper()
	last, err := db.Runs.LastByStatus(jobID, state.RunStatusOK)
	if err != nil {
		t.Fatalf("%s: expected an ok run: %v", phase, err)
	}
	if !strings.Contains(last.StdoutJSON, "GENUINE-RAN") {
		t.Fatalf("%s: last ok run did not execute the genuine binary; stdout=%q", phase, last.StdoutJSON)
	}
	if strings.Contains(last.StdoutJSON, "SUBSTITUTE") {
		t.Fatalf("%s: substitute binary ran; stdout=%q", phase, last.StdoutJSON)
	}
}
