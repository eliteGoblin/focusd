package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/platform/internal/bundle"
	"github.com/eliteGoblin/focusd/platform/internal/core/plugin"
	"github.com/eliteGoblin/focusd/platform/internal/core/state"
	"github.com/eliteGoblin/focusd/platform/internal/testutil"
)

// fakeVerifier is a test integrityVerifier with scripted behaviour.
type fakeVerifier struct {
	restored bool
	err      error
	calls    int
}

func (f *fakeVerifier) VerifyOrRestore(pluginRoot, subdir string) (bool, error) {
	f.calls++
	return f.restored, f.err
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

// TestRunIntegrityRestoredRecordsTamperAndRuns pins AC-1/AC-2: when the
// verify restores a tampered binary, a tamper event is recorded AND the
// (genuine) binary is then executed.
func TestRunIntegrityRestoredRecordsTamperAndRuns(t *testing.T) {
	r := newRunner(t)
	fv := &fakeVerifier{restored: true}
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
	// A tamper-repaired event must be recorded.
	ev, _ := r.DB.Events.Recent(10)
	foundTamper := false
	for _, e := range ev {
		if e.EventType == state.EventTamperRepaired {
			foundTamper = true
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

func (realBundleVerifier) VerifyOrRestore(pluginRoot, subdir string) (bool, error) {
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
