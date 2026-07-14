package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/platdir"
)

// FEATURE 21 (HF1): the daemon's home and the platform's disposable workdir must
// resolve to DIFFERENT roots, and deleting the platform-workdir must leave the
// daemon-home intact and re-establish a FRESH platform-workdir.

// TestResolvePlatformWorkdirDifferentRootAndRecovers walks the same wiring the
// mesh uses at loop() start: resolve the platform-workdir from the pointer,
// wipe it, resolve again → fresh path, daemon-home untouched.
func TestResolvePlatformWorkdirDifferentRootAndRecovers(t *testing.T) {
	sandbox := t.TempDir()
	daemonHome := filepath.Join(sandbox, "daemon-home")
	if err := os.MkdirAll(daemonHome, 0o700); err != nil {
		t.Fatal(err)
	}

	// First resolve: creates a platform-workdir under the sandbox (test mode's
	// support root is the daemon-home's parent), distinct from the daemon-home.
	pw := resolvePlatformWorkdir(mode.Test, daemonHome)
	if pw == "" {
		t.Fatal("resolvePlatformWorkdir returned empty")
	}
	if filepath.Clean(pw) == filepath.Clean(daemonHome) {
		t.Fatal("platform-workdir must NOT equal daemon-home (different roots)")
	}
	if fi, err := os.Stat(pw); err != nil || !fi.IsDir() {
		t.Fatalf("platform-workdir not created: %v", err)
	}

	// Wipe the platform-workdir (the demonstrated HF1 attack).
	if err := os.RemoveAll(pw); err != nil {
		t.Fatal(err)
	}

	// Re-resolve (as a fresh `daemon once` process would): a NEW platform-workdir
	// is established and the pointer rewritten — recovery relies on nothing left
	// inside the deleted folder.
	fresh := resolvePlatformWorkdir(mode.Test, daemonHome)
	if fresh == "" {
		t.Fatal("re-resolve after wipe returned empty")
	}
	if fresh == pw {
		t.Fatal("wiped platform-workdir must be re-created at a FRESH path")
	}
	if fi, err := os.Stat(fresh); err != nil || !fi.IsDir() {
		t.Fatalf("fresh platform-workdir not created: %v", err)
	}
	if platdir.Read(daemonHome) != fresh {
		t.Fatal("pointer must point at the fresh platform-workdir")
	}

	// The daemon-home (binary + state would live here) is untouched by the wipe.
	if _, err := os.Stat(daemonHome); err != nil {
		t.Fatalf("daemon-home must survive a platform-workdir wipe: %v", err)
	}
}

// TestSupportRootForDaemonHomeTestModeIsSandbox: in test mode the support root
// used for (re)creating platform-workdirs is the daemon-home's PARENT — so a
// fresh platform-workdir stays inside the e2e sandbox, never ~/Library.
func TestSupportRootForDaemonHomeTestModeIsSandbox(t *testing.T) {
	sandbox := t.TempDir()
	daemonHome := filepath.Join(sandbox, "daemon-home")
	if got := supportRootForDaemonHome(mode.Test, daemonHome); got != sandbox {
		t.Fatalf("test-mode support root = %q, want sandbox %q", got, sandbox)
	}
}
