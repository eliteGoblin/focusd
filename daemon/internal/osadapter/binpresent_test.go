package osadapter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// testBinPresentDeps returns a default deps set whose seams describe the common
// re-materialize scenario: a MISSING self, valid retained bytes, a passing verify,
// a deterministic fresh name, a real create-only placer (os.WriteFile), and a
// no-op reinstall. The workdir is a real dir strictly under a real supportRoot so
// safeToCreateUnder (which resolves symlinks) passes. Individual tests override
// only the seam under test.
func testBinPresentDeps(t *testing.T) (binPresentDeps, Spec, string) {
	t.Helper()
	supportRoot := t.TempDir()
	workdir := filepath.Join(supportRoot, "hidden-workdir")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := Spec{
		Mode:     mode.User,
		SelfPath: filepath.Join(workdir, "old-binary"),
		Workdir:  workdir,
	}
	d := binPresentDeps{
		selfExists:    func(string) (bool, error) { return false, nil }, // gone by default
		readSelfBytes: func() ([]byte, error) { return []byte("SIGNED-DAEMON-BYTES"), nil },
		verify:        func([]byte) (bool, error) { return true, nil },
		randName:      func() string { return "fresh.disguised.name.deadbeef01" },
		place:         func(b []byte, dst string) error { return os.WriteFile(dst, b, 0o755) },
		reinstall:     func(Spec) error { return nil },
		supportRoot:   supportRoot,
	}
	return d, spec, workdir
}

// (a) A deleted self is re-materialized at a FRESH path directly inside the
// workdir, and the bytes are actually written.
func TestEnsureBinaryPresent_MissingSelfPlacesFreshBinary(t *testing.T) {
	d, spec, workdir := testBinPresentDeps(t)
	var placedDst string
	var placedBytes []byte
	d.place = func(b []byte, dst string) error {
		placedDst, placedBytes = dst, b
		return os.WriteFile(dst, b, 0o755)
	}

	newSelf, changed, err := ensureBinaryPresent(d, spec, true)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !changed || newSelf == "" {
		t.Fatalf("want a re-materialize, got changed=%v newSelf=%q", changed, newSelf)
	}
	if filepath.Dir(newSelf) != workdir {
		t.Errorf("fresh binary %q is not directly in workdir %q", newSelf, workdir)
	}
	if newSelf == spec.SelfPath {
		t.Error("fresh path must differ from the deleted (old) path")
	}
	if placedDst != newSelf {
		t.Errorf("placed at %q but returned %q", placedDst, newSelf)
	}
	if string(placedBytes) != "SIGNED-DAEMON-BYTES" {
		t.Errorf("placed the wrong bytes: %q", placedBytes)
	}
	if _, statErr := os.Stat(newSelf); statErr != nil {
		t.Errorf("fresh binary not on disk: %v", statErr)
	}
}

// (b) When self is still present it is a cheap no-op — nothing placed, no
// reinstall.
func TestEnsureBinaryPresent_PresentSelfNoOp(t *testing.T) {
	d, spec, _ := testBinPresentDeps(t)
	d.selfExists = func(string) (bool, error) { return true, nil }
	placeCalled, reinstallCalled := false, false
	d.place = func([]byte, string) error { placeCalled = true; return nil }
	d.reinstall = func(Spec) error { reinstallCalled = true; return nil }

	newSelf, changed, err := ensureBinaryPresent(d, spec, true)
	if err != nil || changed || newSelf != "" {
		t.Fatalf("want no-op, got (%q, %v, %v)", newSelf, changed, err)
	}
	if placeCalled || reinstallCalled {
		t.Error("steady-state path must not place or reinstall")
	}
}

// (c) Bytes that fail verification are REFUSED before any write.
func TestEnsureBinaryPresent_VerifyFailRefuses(t *testing.T) {
	d, spec, _ := testBinPresentDeps(t)
	d.verify = func([]byte) (bool, error) { return false, nil }
	placeCalled := false
	d.place = func([]byte, string) error { placeCalled = true; return nil }

	newSelf, changed, err := ensureBinaryPresent(d, spec, true)
	if !errors.Is(err, errUnverifiedSelf) {
		t.Fatalf("want errUnverifiedSelf, got %v", err)
	}
	if changed || newSelf != "" {
		t.Error("a refusal must not report a change")
	}
	if placeCalled {
		t.Error("must NOT place bytes that failed verification")
	}
}

// (d) All three mesh plists are repointed at the fresh binary (via the reinstall
// spec's SelfPath), rendered through the real Plist template.
func TestEnsureBinaryPresent_AllThreePlistsRepointed(t *testing.T) {
	d, spec, _ := testBinPresentDeps(t)
	// A populated roster makes Plist emit the prod Program=<SelfPath> shape.
	spec.Roster = []string{"com.vendor.alpha", "com.vendor.bravo", "com.vendor.charlie"}
	var gotSpec Spec
	d.reinstall = func(ns Spec) error { gotSpec = ns; return nil }

	newSelf, changed, err := ensureBinaryPresent(d, spec, true)
	if err != nil || !changed {
		t.Fatalf("want a re-materialize, got changed=%v err=%v", changed, err)
	}
	if gotSpec.SelfPath != newSelf {
		t.Fatalf("reinstall spec.SelfPath = %q, want the fresh path %q", gotSpec.SelfPath, newSelf)
	}
	for _, r := range AllRoles {
		pl := Plist(gotSpec, r)
		if !strings.Contains(pl, newSelf) {
			t.Errorf("role %s plist does not point at the fresh binary %q", r, newSelf)
		}
	}
}

// (e) The heal is strictly CREATE-ONLY: unrelated workdir contents (a state.db
// file and a plugins dir) survive — proving no RemoveAll-class blast. The deps
// struct has no delete seam at all; this asserts that against a real FS.
func TestEnsureBinaryPresent_CreateOnlyNoCollateralDelete(t *testing.T) {
	d, spec, workdir := testBinPresentDeps(t)
	sentinelFile := filepath.Join(workdir, "state.db")
	if err := os.WriteFile(sentinelFile, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinelDir := filepath.Join(workdir, "plugins")
	if err := os.MkdirAll(sentinelDir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, changed, err := ensureBinaryPresent(d, spec, true)
	if err != nil || !changed {
		t.Fatalf("want a re-materialize, got changed=%v err=%v", changed, err)
	}
	if _, e := os.Stat(sentinelFile); e != nil {
		t.Errorf("sentinel state.db was deleted by the heal: %v", e)
	}
	if _, e := os.Stat(sentinelDir); e != nil {
		t.Errorf("sentinel plugins/ dir was deleted by the heal: %v", e)
	}
}

// (f) Containment: if the workdir is not strictly under supportRoot, the placement
// is refused (belt-and-suspenders against a corrupted spec.Workdir).
func TestEnsureBinaryPresent_RefusesWhenWorkdirOutsideSupportRoot(t *testing.T) {
	d, spec, _ := testBinPresentDeps(t)
	d.supportRoot = t.TempDir() // unrelated tree — workdir is no longer nested under it
	placeCalled := false
	d.place = func([]byte, string) error { placeCalled = true; return nil }

	newSelf, changed, err := ensureBinaryPresent(d, spec, true)
	if !errors.Is(err, errUnsafeCreatePath) {
		t.Fatalf("want errUnsafeCreatePath, got %v", err)
	}
	if changed || newSelf != "" || placeCalled {
		t.Error("must refuse to place outside containment")
	}
}

// (g) A worker that does NOT hold the platform singleton lock is a no-op — it does
// not even stat self (so roles A and B don't both act).
func TestEnsureBinaryPresent_NonLockHolderNoOp(t *testing.T) {
	d, spec, _ := testBinPresentDeps(t)
	statCalled := false
	d.selfExists = func(string) (bool, error) { statCalled = true; return false, nil }

	newSelf, changed, err := ensureBinaryPresent(d, spec, false)
	if err != nil || changed || newSelf != "" {
		t.Fatalf("want no-op, got (%q, %v, %v)", newSelf, changed, err)
	}
	if statCalled {
		t.Error("non-holder must short-circuit BEFORE stat'ing self")
	}
}

// (h) Test mode never touches a real launchd mesh — no-op, not even a stat.
func TestEnsureBinaryPresent_TestModeNoOp(t *testing.T) {
	d, spec, _ := testBinPresentDeps(t)
	spec.Mode = mode.Test
	statCalled := false
	d.selfExists = func(string) (bool, error) { statCalled = true; return false, nil }

	newSelf, changed, err := ensureBinaryPresent(d, spec, true)
	if err != nil || changed || newSelf != "" {
		t.Fatalf("want no-op, got (%q, %v, %v)", newSelf, changed, err)
	}
	if statCalled {
		t.Error("test mode must short-circuit BEFORE stat'ing self")
	}
}

// An ambiguous stat failure (not a clean ENOENT) must NOT be treated as "deleted"
// — the heal aborts rather than re-materializing on a guess.
func TestEnsureBinaryPresent_StatErrorAborts(t *testing.T) {
	d, spec, _ := testBinPresentDeps(t)
	statErr := errors.New("permission denied")
	d.selfExists = func(string) (bool, error) { return false, statErr }
	placeCalled := false
	d.place = func([]byte, string) error { placeCalled = true; return nil }

	newSelf, changed, err := ensureBinaryPresent(d, spec, true)
	if !errors.Is(err, statErr) {
		t.Fatalf("want the stat error surfaced, got %v", err)
	}
	if changed || newSelf != "" || placeCalled {
		t.Error("an ambiguous stat failure must not re-materialize")
	}
}

// No retained-fd source (empty bytes) is a refusal, not a silent empty placement.
func TestEnsureBinaryPresent_EmptySourceRefuses(t *testing.T) {
	d, spec, _ := testBinPresentDeps(t)
	d.readSelfBytes = func() ([]byte, error) { return nil, nil }
	placeCalled := false
	d.place = func([]byte, string) error { placeCalled = true; return nil }

	_, changed, err := ensureBinaryPresent(d, spec, true)
	if !errors.Is(err, errNoSelfSource) {
		t.Fatalf("want errNoSelfSource, got %v", err)
	}
	if changed || placeCalled {
		t.Error("empty source must not place anything")
	}
}

// The binary IS placed even if the launchd re-bootstrap fails: the caller must
// adopt the fresh path (the next tick's EnsureAll retries the plist side).
func TestEnsureBinaryPresent_ReinstallErrStillAdoptsNewPath(t *testing.T) {
	d, spec, workdir := testBinPresentDeps(t)
	d.reinstall = func(Spec) error { return errors.New("bootstrap boom") }

	newSelf, changed, err := ensureBinaryPresent(d, spec, true)
	if err == nil {
		t.Fatal("want the reinstall error surfaced")
	}
	if !changed || newSelf == "" {
		t.Fatal("the binary was placed; caller must adopt newSelf even if launchd rebuild failed")
	}
	if filepath.Dir(newSelf) != workdir {
		t.Errorf("fresh binary %q not directly in workdir %q", newSelf, workdir)
	}
	if _, statErr := os.Stat(newSelf); statErr != nil {
		t.Errorf("binary should be on disk despite the reinstall failure: %v", statErr)
	}
}

// safeToCreateUnder is the create-side containment guard.
func TestSafeToCreateUnder(t *testing.T) {
	root := t.TempDir()
	wd := filepath.Join(root, "wd")
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(wd, "bin.x")
	other := t.TempDir()

	cases := []struct {
		name                        string
		newPath, workdir, supportRt string
		want                        bool
	}{
		{"happy path", good, wd, root, true},
		{"relative newPath", "rel/bin", wd, root, false},
		{"relative workdir", good, "wd", root, false},
		{"relative supportRoot", good, wd, "root", false},
		{"traversal base", wd + "/..", wd, root, false},
		{"workdir equals supportRoot (not strictly under)", filepath.Join(root, "bin"), root, root, false},
		{"workdir outside supportRoot", good, wd, other, false},
		{"empty newPath", "", wd, root, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := safeToCreateUnder(c.newPath, c.workdir, c.supportRt); got != c.want {
				t.Errorf("safeToCreateUnder(%q,%q,%q) = %v, want %v",
					c.newPath, c.workdir, c.supportRt, got, c.want)
			}
		})
	}
}
