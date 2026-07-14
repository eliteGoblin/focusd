package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/platform/internal/bundle"
	"github.com/eliteGoblin/focusd/platform/internal/core/plugin"
	"github.com/eliteGoblin/focusd/platform/internal/core/state"
	"github.com/eliteGoblin/focusd/platform/internal/testutil"
)

// fakeVerifier is a test integrityVerifier with scripted behaviour.
type fakeVerifier struct {
	restored   bool
	wantPrefix string
	gotPrefix  string
	err        error
	calls      int
}

func (f *fakeVerifier) VerifyOrRestore(pluginRoot, subdir string) (bool, string, string, error) {
	f.calls++
	return f.restored, f.wantPrefix, f.gotPrefix, f.err
}

// TestRunIntegrityErrorDoesNotExec pins AC: when the point-of-use
// integrity check errors, the runner must NOT exec the (possibly tampered)
// binary. It records an error run + a check-failed event and returns.
func TestRunIntegrityErrorDoesNotExec(t *testing.T) {
	r := newRunner(t)
	fv := &fakeVerifier{err: errors.New("disk read failed")}
	r.WithVerifier(fv)

	// A plugin that would PASS (exit 0) if it ran — proving the refusal is
	// driven by the verify error, not the plugin.
	p := testutil.ScriptPlugin(t, "ok-plugin", `echo '{"status":"ok"}'
exit 0`)
	out, err := r.Run(context.Background(), Job{ID: "j1"}, p, "scheduler")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Status != state.RunStatusError {
		t.Fatalf("verify error must yield error status, got %+v", out)
	}
	if fv.calls == 0 {
		t.Fatal("verifier was not consulted")
	}
	// An error run row must be recorded.
	last, herr := r.DB.Runs.LastByStatus("j1", state.RunStatusError)
	if herr != nil {
		t.Fatalf("expected recorded error run: %v", herr)
	}
	if last.StdoutJSON != "" {
		t.Error("binary must not have run (no stdout)")
	}
	// A check-failed event must be recorded.
	ev, _ := r.DB.Events.Recent(10)
	foundEvent := false
	for _, e := range ev {
		if e.EventType == state.EventIntegrityCheckFailed {
			foundEvent = true
		}
	}
	if !foundEvent {
		t.Error("expected plugin_integrity_check_failed event")
	}
}

// TestRunIntegrityErrorTerminalNoInTickRetry pins Fix 2: a verify error on a
// job with Retry>0 must record EXACTLY ONE error run + ONE check-failed event
// and NOT exec — the integrity-verify error is terminal for this tick (retry
// is the NEXT scheduled tick). Without the terminal marker the retry loop
// would re-run the verifier Retry+1 times, spamming runs/events on a
// transient FS fault.
func TestRunIntegrityErrorTerminalNoInTickRetry(t *testing.T) {
	r := newRunner(t)
	fv := &fakeVerifier{err: errors.New("disk read failed")}
	r.WithVerifier(fv)

	p := testutil.ScriptPlugin(t, "ok-plugin", `echo '{"status":"ok"}'
exit 0`)
	const retry = 3
	out, err := r.Run(context.Background(), Job{ID: "j1", Retry: retry}, p, "scheduler")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Status != state.RunStatusError {
		t.Fatalf("verify error must yield error status, got %+v", out)
	}
	// The verifier must have been consulted EXACTLY once — no in-tick retries.
	if fv.calls != 1 {
		t.Errorf("verifier called %d times, want exactly 1 (no in-tick retry)", fv.calls)
	}
	// Exactly ONE error run recorded (not Retry+1).
	hist, herr := r.DB.Runs.History("j1", 10)
	if herr != nil {
		t.Fatalf("History: %v", herr)
	}
	if len(hist) != 1 {
		t.Errorf("expected exactly 1 recorded run, got %d", len(hist))
	}
	for _, h := range hist {
		if h.Status != state.RunStatusError {
			t.Errorf("recorded run status = %q, want error", h.Status)
		}
		if h.StdoutJSON != "" {
			t.Error("binary must not have run (no stdout)")
		}
	}
	// Exactly ONE plugin_integrity_check_failed event (not Retry+1).
	ev, eerr := r.DB.Events.Recent(50)
	if eerr != nil {
		t.Fatalf("Recent: %v", eerr)
	}
	failedEvents := 0
	for _, e := range ev {
		if e.EventType == state.EventIntegrityCheckFailed {
			failedEvents++
		}
	}
	if failedEvents != 1 {
		t.Errorf("expected exactly 1 plugin_integrity_check_failed event, got %d", failedEvents)
	}
}

// TestRunIntegrityRestoredRecordsTamperAndRuns pins AC-1/AC-2: when the
// verify restores a tampered binary, a tamper event is recorded AND the
// (genuine) binary is then executed.
func TestRunIntegrityRestoredRecordsTamperAndRuns(t *testing.T) {
	r := newRunner(t)
	fv := &fakeVerifier{restored: true, wantPrefix: "aabbccddeeff", gotPrefix: "112233445566"}
	r.WithVerifier(fv)

	p := testutil.ScriptPlugin(t, "ok-plugin", `echo '{"status":"ok"}'
exit 0`)
	out, err := r.Run(context.Background(), Job{ID: "j1"}, p, "scheduler")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Status != state.RunStatusOK {
		t.Fatalf("genuine binary must run after restore, got %+v", out)
	}
	// A tamper-repaired event must be recorded, carrying the sha prefixes
	// the verifier reported (and never a path).
	ev, _ := r.DB.Events.Recent(10)
	foundTamper := false
	for _, e := range ev {
		if e.EventType == state.EventTamperRepaired {
			foundTamper = true
			if !strings.Contains(e.DetailsJSON, "aabbccddeeff") ||
				!strings.Contains(e.DetailsJSON, "112233445566") {
				t.Errorf("tamper event missing sha prefixes: %s", e.DetailsJSON)
			}
		}
	}
	if !foundTamper {
		t.Error("expected plugin_tamper_repaired event after restore")
	}
	// The genuine run was recorded ok.
	if _, herr := r.DB.Runs.LastByStatus("j1", state.RunStatusOK); herr != nil {
		t.Errorf("expected an ok run after restore: %v", herr)
	}
}

// realBundleVerifier exercises the actual bundle.VerifyOrRestore through
// the runner's pluginRoot/subdir decomposition — closing the gap that the
// fakeVerifier tests don't cover (H1).
type realBundleVerifier struct{}

func (realBundleVerifier) VerifyOrRestore(pluginRoot, subdir string) (bool, string, string, error) {
	return bundle.VerifyOrRestore(pluginRoot, subdir)
}

// TestRunIntegrityRealBundlePathDecomposition pins H1: the runner derives
// pluginRoot+subdir from p.Dir correctly, so the REAL bundle verifier
// scopes to exactly the one plugin about to run. Tamper a bundled plugin's
// binary on disk, point a Discovered at it, and confirm the run restores it.
func TestRunIntegrityRealBundlePathDecomposition(t *testing.T) {
	if !bundle.HasAny() {
		t.Skip("no bundled plugins in this build; skipping")
	}
	root := t.TempDir()
	if _, err := bundle.ExtractTo(root); err != nil {
		t.Fatalf("extract bundle: %v", err)
	}
	// Find a bundled plugin subdir with an extensionless binary.
	var subdir, binPath string
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(root, e.Name()))
		for _, f := range files {
			if !f.IsDir() && filepath.Ext(f.Name()) == "" && !containsDot(f.Name()) {
				subdir = e.Name()
				binPath = filepath.Join(root, e.Name(), f.Name())
			}
		}
	}
	if subdir == "" {
		t.Skip("no extensionless bundled binary; skipping")
	}
	genuine, _ := os.ReadFile(binPath)

	// Tamper: overwrite with a do-nothing stand-in.
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	r := newRunner(t).WithVerifier(realBundleVerifier{})
	// A Discovered whose Dir is <root>/<subdir>, BinaryPath the on-disk bin.
	// We don't actually exec the genuine bundled binary's protocol here —
	// only assert the restore happened (the run may error on protocol, which
	// is fine; the integrity restore is what's under test).
	p := plugin.Discovered{
		Manifest: &plugin.Manifest{
			ID: subdir, Name: subdir, Version: "1.0.0", Type: plugin.TypeJob,
			ProtocolVersion: "1", Entrypoint: "./" + filepath.Base(binPath),
			RunAs: plugin.RunAsCurrentUser,
		},
		Dir: filepath.Join(root, subdir), BinaryPath: binPath, OK: true,
	}
	_, _ = r.Run(context.Background(), Job{ID: subdir + "-job"}, p, "scheduler")

	got, _ := os.ReadFile(binPath)
	if string(got) != string(genuine) {
		t.Fatal("real bundle verify did not restore the tampered binary via runner decomposition")
	}
	// A tamper event must have been recorded.
	ev, _ := r.DB.Events.Recent(10)
	found := false
	for _, e := range ev {
		if e.EventType == state.EventTamperRepaired {
			found = true
		}
	}
	if !found {
		t.Error("expected plugin_tamper_repaired event")
	}
}

func containsDot(s string) bool {
	for _, c := range s {
		if c == '.' {
			return true
		}
	}
	return false
}

// TestRunTOCTOUGuardRefusesSwapBetweenVerifyAndExec pins FEATURE 23, Fix 2:
// the point-of-use verify passes, but the binary is swapped in the tiny window
// before exec. The pre-exec re-hash guard MUST detect the change and refuse to
// run the substitute — recording an error run + a check-failed event, never
// executing the swapped-in binary.
func TestRunTOCTOUGuardRefusesSwapBetweenVerifyAndExec(t *testing.T) {
	r := newRunner(t)
	// Verifier reports clean (no restore, no error): the on-disk binary matched
	// genuine at verify time. The swap happens AFTER, via the afterPin seam.
	r.WithVerifier(&fakeVerifier{})

	// A plugin that, if the SUBSTITUTE ran, would print a tell-tale marker and
	// exit 0. The guard must stop it before it ever execs.
	p := testutil.ScriptPlugin(t, "guarded", `echo '{"status":"ok"}'
exit 0`)

	swapped := false
	r.afterPin = func(binPath string) {
		// Overwrite the just-verified binary with a different program,
		// simulating a rename-swap landing in the verify→exec window.
		if err := os.WriteFile(binPath, []byte("#!/bin/sh\necho SUBSTITUTE-RAN\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("simulate swap: %v", err)
		}
		swapped = true
	}

	out, err := r.Run(context.Background(), Job{ID: "j1", Retry: 2}, p, "scheduler")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !swapped {
		t.Fatal("afterPin seam never fired; guard was not exercised")
	}
	if out.Status != state.RunStatusError {
		t.Fatalf("a post-verify swap must be refused (error status), got %+v", out)
	}
	// The substitute must NOT have executed: no run row carries its stdout.
	hist, herr := r.DB.Runs.History("j1", 10)
	if herr != nil {
		t.Fatalf("History: %v", herr)
	}
	if len(hist) != 1 {
		t.Fatalf("expected exactly 1 recorded run (terminal, no in-tick retry), got %d", len(hist))
	}
	if hist[0].StdoutJSON != "" || strings.Contains(hist[0].StdoutJSON, "SUBSTITUTE") {
		t.Errorf("substitute binary must not have run; stdout=%q", hist[0].StdoutJSON)
	}
	// A check-failed event must be recorded so the refusal is auditable.
	ev, _ := r.DB.Events.Recent(10)
	found := false
	for _, e := range ev {
		if e.EventType == state.EventIntegrityCheckFailed {
			found = true
		}
	}
	if !found {
		t.Error("expected plugin_integrity_check_failed event after a detected swap")
	}
}

// TestHashFileDetectsContentChange is a focused unit test for the pin
// primitive the TOCTOU guard relies on: a re-hash of the same path after a
// content change must differ (FEATURE 23, Fix 2).
func TestHashFileDetectsContentChange(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(p, []byte("genuine"), 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := hashFile(p)
	if err != nil {
		t.Fatalf("hashFile: %v", err)
	}
	if err := os.WriteFile(p, []byte("substitute"), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := hashFile(p)
	if err != nil {
		t.Fatalf("hashFile: %v", err)
	}
	if a == b {
		t.Fatal("hashFile must differ after a content change")
	}
	// A missing file is an error (the guard treats it as a refusal).
	if _, err := hashFile(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("hashFile of a missing file should error")
	}
}

// TestRunNoVerifierRunsAsBefore: with no verifier wired, behaviour is
// unchanged — the plugin runs exactly as it did pre-FEATURE-15.
func TestRunNoVerifierRunsAsBefore(t *testing.T) {
	r := newRunner(t) // no verifier
	p := testutil.ScriptPlugin(t, "ok-plugin", `echo '{"status":"ok"}'
exit 0`)
	out, err := r.Run(context.Background(), Job{ID: "j1"}, p, "scheduler")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Status != state.RunStatusOK {
		t.Fatalf("no-verifier run must succeed, got %+v", out)
	}
}
