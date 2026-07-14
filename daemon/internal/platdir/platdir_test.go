package platdir

import (
	"os"
	"path/filepath"
	"testing"
)

// --- Containment guard (SafeTarget) --------------------------------------

func TestSafeTargetRejectsBadTargets(t *testing.T) {
	const supportRoot = "/s"
	const daemonHome = "/s/a/dh"

	cases := []struct {
		name   string
		target string
		want   bool
	}{
		{"relative target", "relative/pw", false},
		{"outside support root", "/other/pw", false},
		{"support root itself", "/s", false},
		{"escape via ..", "/s/../pw", false},
		{"ancestor of daemon-home", "/s/a", false},          // wiping this would take dh
		{"daemon-home itself", "/s/a/dh", false},            // never place platform ON dh
		{"nested inside daemon-home", "/s/a/dh/pw", false},  // reverse nesting re-couples lifetimes
		{"deeper inside daemon-home", "/s/a/dh/x/y", false}, // still under dh → rejected
		{"valid sibling under root", "/s/a/pw", true},       // strictly under, not an ancestor
		{"valid nested elsewhere", "/s/other/pw", true},     // strictly under, unrelated to dh
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SafeTarget(c.target, supportRoot, daemonHome); got != c.want {
				t.Fatalf("SafeTarget(%q,%q,%q) = %v, want %v",
					c.target, supportRoot, daemonHome, got, c.want)
			}
		})
	}
}

func TestSafeTargetRequiresAbsolutePaths(t *testing.T) {
	if SafeTarget("/s/pw", "relative-root", "/s/dh") {
		t.Fatal("a relative supportRoot must be refused")
	}
	if SafeTarget("", "/s", "/s/dh") {
		t.Fatal("an empty target must be refused")
	}
}

// --- Resolve: present / missing→recreate ---------------------------------

// TestResolveCreatesWhenPointerMissing: no pointer yet → a fresh, sentinel-
// marked platform-workdir is created under supportRoot and the pointer records
// it.
func TestResolveCreatesWhenPointerMissing(t *testing.T) {
	root := t.TempDir()
	daemonHome := filepath.Join(root, "daemon-home")
	if err := os.MkdirAll(daemonHome, 0o700); err != nil {
		t.Fatal(err)
	}

	pw, err := Resolve(daemonHome, root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if fi, serr := os.Stat(pw); serr != nil || !fi.IsDir() {
		t.Fatalf("platform-workdir not created: %v", serr)
	}
	if !IsPlatformWorkdir(pw) {
		t.Fatal("resolved platform-workdir must carry the sentinel")
	}
	if Read(daemonHome) != pw {
		t.Fatalf("pointer not written: read %q, want %q", Read(daemonHome), pw)
	}
}

// TestResolveKeepsHealthyPointer: a present pointer whose target exists + is
// safe is returned UNCHANGED (no churn when nothing is wrong).
func TestResolveKeepsHealthyPointer(t *testing.T) {
	root := t.TempDir()
	daemonHome := filepath.Join(root, "daemon-home")
	if err := os.MkdirAll(daemonHome, 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := Resolve(daemonHome, root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Resolve(daemonHome, root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("healthy pointer must be stable: %q then %q", first, second)
	}
}

// TestResolveRecreatesWhenTargetWiped: the platform-workdir is deleted (the
// exact HF1 attack) → Resolve creates a FRESH one at a NEW path and rewrites the
// pointer. Recovery relies on nothing left inside the deleted folder.
func TestResolveRecreatesWhenTargetWiped(t *testing.T) {
	root := t.TempDir()
	daemonHome := filepath.Join(root, "daemon-home")
	if err := os.MkdirAll(daemonHome, 0o700); err != nil {
		t.Fatal(err)
	}
	old, err := Resolve(daemonHome, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}

	fresh, err := Resolve(daemonHome, root)
	if err != nil {
		t.Fatalf("Resolve after wipe: %v", err)
	}
	if fresh == old {
		t.Fatal("a wiped target must be re-created at a FRESH path")
	}
	if fi, serr := os.Stat(fresh); serr != nil || !fi.IsDir() {
		t.Fatalf("fresh platform-workdir not created: %v", serr)
	}
	if Read(daemonHome) != fresh {
		t.Fatal("pointer must be rewritten to the fresh path")
	}
	if _, serr := os.Stat(daemonHome); serr != nil {
		t.Fatal("daemon-home must be untouched by the platform-workdir wipe")
	}
}

// TestResolveRecreatesWhenTargetUnsafe: a hostile pointer that escapes the
// support root is not trusted — Resolve recreates a safe one.
func TestResolveRecreatesWhenTargetUnsafe(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	daemonHome := filepath.Join(root, "daemon-home")
	if err := os.MkdirAll(daemonHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Write(daemonHome, outside); err != nil { // points OUTSIDE supportRoot
		t.Fatal(err)
	}

	pw, err := Resolve(daemonHome, root)
	if err != nil {
		t.Fatal(err)
	}
	if pw == outside {
		t.Fatal("an out-of-root pointer target must NOT be trusted")
	}
	if !SafeTarget(pw, root, daemonHome) {
		t.Fatal("the recreated target must pass the containment guard")
	}
}

// TestResolveRejectsSymlinkEscape is the HF1 HIGH regression: a pointer target
// that is a SYMLINK whose text is lexically under the support root but whose real
// destination ESCAPES it. SafeTarget is lexical (+ os.Stat follows the link, so it
// sees a valid dir), so only the EvalSymlinks re-check in Resolve can catch this.
// Resolve must refuse the escaping symlink and recreate a fresh, real workdir.
func TestResolveRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir() // a REAL directory OUTSIDE the support root
	daemonHome := filepath.Join(root, "daemon-home")
	if err := os.MkdirAll(daemonHome, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".escape-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := Write(daemonHome, link); err != nil {
		t.Fatal(err)
	}

	pw, err := Resolve(daemonHome, root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if filepath.Clean(pw) == filepath.Clean(link) {
		t.Fatal("a symlink whose real target escapes the support root must NOT be trusted")
	}
	// The recreated workdir resolves strictly under the support root.
	rpw, _ := filepath.EvalSymlinks(pw)
	rout, _ := filepath.EvalSymlinks(outside)
	if rpw == rout {
		t.Fatal("Resolve must recreate a fresh workdir, never the escaping target")
	}
	if fi, serr := os.Stat(pw); serr != nil || !fi.IsDir() {
		t.Fatalf("fresh platform-workdir not created: %v", serr)
	}
}

// TestResolveRejectsSymlinkToDaemonHome is the HF1 HIGH regression's second face:
// a pointer that is a symlink resolving to the DAEMON-HOME itself. Trusting it
// would re-couple the platform-workdir and daemon-home lifetimes (a platform wipe
// would take the daemon down). The EvalSymlinks re-check must reject it and
// recreate a separate workdir.
func TestResolveRejectsSymlinkToDaemonHome(t *testing.T) {
	root := t.TempDir()
	daemonHome := filepath.Join(root, "daemon-home")
	if err := os.MkdirAll(daemonHome, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".dh-link")
	if err := os.Symlink(daemonHome, link); err != nil {
		t.Fatal(err)
	}
	if err := Write(daemonHome, link); err != nil {
		t.Fatal(err)
	}

	pw, err := Resolve(daemonHome, root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	rpw, _ := filepath.EvalSymlinks(pw)
	rdh, _ := filepath.EvalSymlinks(daemonHome)
	if rpw == rdh {
		t.Fatal("a symlink resolving to daemon-home must NOT be trusted (shared-fate defect)")
	}
	if fi, serr := os.Stat(pw); serr != nil || !fi.IsDir() {
		t.Fatalf("fresh platform-workdir not created: %v", serr)
	}
}

// TestDaemonAndPlatformResolveToDifferentRoots pins the core HF1 property: the
// daemon's own home and the platform's disposable workdir are DIFFERENT roots —
// neither equals the other and the platform-workdir is not an ancestor of
// daemon-home, so a platform wipe cannot take the daemon down with it.
func TestDaemonAndPlatformResolveToDifferentRoots(t *testing.T) {
	root := t.TempDir()
	daemonHome := filepath.Join(root, "daemon-home")
	if err := os.MkdirAll(daemonHome, 0o700); err != nil {
		t.Fatal(err)
	}
	pw, err := Resolve(daemonHome, root)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(pw) == filepath.Clean(daemonHome) {
		t.Fatal("platform-workdir must NOT equal daemon-home")
	}
	// The guard already enforces "not an ancestor of daemon-home"; assert it here
	// too as the acceptance property.
	if !SafeTarget(pw, root, daemonHome) {
		t.Fatal("platform-workdir must be a safe, independent root")
	}
}
