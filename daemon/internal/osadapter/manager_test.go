package osadapter

import (
	"strings"
	"testing"
	"time"
)

type fakeCtl struct {
	loadedSet       map[string]bool
	boots           []string
	bouts           []string
	bootstrapFailOn string           // plist path that should fail bootstrap (always)
	bootstrapFailN  map[string]int   // plist path → # of times to fail before succeeding (transient)
	bootoutErrOn    map[string]error // label → error to return from bootout
}

func newFakeCtl() *fakeCtl { return &fakeCtl{loadedSet: map[string]bool{}} }

func (f *fakeCtl) loaded(l string) bool { return f.loadedSet[l] }
func (f *fakeCtl) bootstrap(pp string) error {
	if pp == f.bootstrapFailOn {
		return errCtlSynthetic
	}
	// Transient failure: model launchd's async "already-loaded"/EIO window that a
	// robustReload retry (bootout+backoff+bootstrap) is meant to absorb.
	if f.bootstrapFailN != nil && f.bootstrapFailN[pp] > 0 {
		f.bootstrapFailN[pp]--
		return errCtlSynthetic
	}
	f.boots = append(f.boots, pp)
	f.loadedSet[labelFromPath(pp)] = true
	return nil
}
func (f *fakeCtl) bootout(l string) error {
	f.bouts = append(f.bouts, l)
	delete(f.loadedSet, l)
	if err, ok := f.bootoutErrOn[l]; ok {
		return err
	}
	return nil
}

var errCtlSynthetic = errSentinel("synthetic ctl failure")

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

type fakeFS struct {
	files       map[string]string
	writeFailOn string // path that should fail write
}

func newFakeFS() *fakeFS                    { return &fakeFS{files: map[string]string{}} }
func (f *fakeFS) plistPath(l string) string { return "/p/" + l + ".plist" }
func (f *fakeFS) write(p, c string) error {
	if p == f.writeFailOn {
		return errSentinel("synthetic write failure")
	}
	f.files[p] = c
	return nil
}
func (f *fakeFS) remove(p string) error { delete(f.files, p); return nil }
func labelFromPath(p string) string {
	p = strings.TrimPrefix(p, "/p/")
	return strings.TrimSuffix(p, ".plist")
}

// fakeRoster is an in-memory rosterIO (FEATURE 10 / ADR-0014). present
// models a real file on disk; corrupt forces readRoster to error so the
// self-heal-from-memory path (ensureAll rewrites) is exercised.
type fakeRoster struct {
	labels  []string
	present bool
	corrupt bool
	writes  int
}

func (r *fakeRoster) writeRoster(labels []string) error {
	r.labels = append([]string(nil), labels...)
	r.present = true
	r.corrupt = false
	r.writes++
	return nil
}
func (r *fakeRoster) readRoster() ([]string, error) {
	if !r.present {
		return nil, errSentinel("roster missing")
	}
	if r.corrupt {
		return nil, errSentinel("roster corrupt")
	}
	return r.labels, nil
}
func (r *fakeRoster) removeRoster() error { r.present = false; return nil }

func spec() Spec {
	return Spec{SelfPath: "/d/daemon", Workdir: "/wd", Github: "o/r",
		Asset: "platform-darwin-arm64", Interval: 10 * time.Second}
}

func TestInstallAllWritesAndLoadsThree(t *testing.T) {
	c, fs, rs := newFakeCtl(), newFakeFS(), &fakeRoster{}
	if err := installAll(spec(), c, fs, rs); err != nil {
		t.Fatal(err)
	}
	if len(fs.files) != 3 || len(c.boots) != 3 {
		t.Fatalf("want 3 plists+3 bootstraps, got %d/%d", len(fs.files), len(c.boots))
	}
	for _, r := range AllRoles {
		if !c.loaded(spec().Label(r)) {
			t.Fatalf("role %s not loaded", r)
		}
	}
	// Install writes the masked roster (the 3 labels in AllRoles order).
	if !rs.present || len(rs.labels) != 3 {
		t.Fatalf("install must persist the 3-label roster, got present=%v labels=%v", rs.present, rs.labels)
	}
	if !sameRoster(rs.labels, rosterLabels(spec())) {
		t.Fatalf("persisted roster %v != spec labels %v", rs.labels, rosterLabels(spec()))
	}
}

func TestEnsureAllRecreatesOnlyMissing(t *testing.T) {
	c, fs, rs := newFakeCtl(), newFakeFS(), &fakeRoster{}
	_ = installAll(spec(), c, fs, rs)
	// Simulate the user killing role A's entry.
	_ = c.bootout(spec().Label(RoleA))
	delete(fs.files, fs.plistPath(spec().Label(RoleA)))

	rec, err := ensureAll(spec(), c, fs, rs)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec) != 1 || rec[0] != RoleA {
		t.Fatalf("only A should be recreated, got %v", rec)
	}
	if !c.loaded(spec().Label(RoleA)) {
		t.Fatal("A must be loaded again after ensure")
	}
	// Second ensure is a no-op (idempotent).
	rec2, _ := ensureAll(spec(), c, fs, rs)
	if len(rec2) != 0 {
		t.Fatalf("ensure must be idempotent, recreated %v", rec2)
	}
}

// TestEnsureAllHealsRosterFromMemory asserts acceptance #4 at the osadapter
// seam: when the roster file is missing/corrupt, ensureAll rewrites it from
// the in-memory (Spec) roster while the mesh keeps running.
func TestEnsureAllHealsRosterFromMemory(t *testing.T) {
	c, fs, rs := newFakeCtl(), newFakeFS(), &fakeRoster{}
	_ = installAll(spec(), c, fs, rs)
	writesAfterInstall := rs.writes

	// Tamper: corrupt the file so readRoster errors.
	rs.corrupt = true
	if _, err := ensureAll(spec(), c, fs, rs); err != nil {
		t.Fatal(err)
	}
	if rs.writes != writesAfterInstall+1 {
		t.Fatalf("corrupt roster must be rewritten once, writes=%d (was %d)", rs.writes, writesAfterInstall)
	}
	if rs.corrupt || !rs.present {
		t.Fatal("roster must be healed (present, not corrupt) after ensure")
	}

	// Delete: missing file is likewise rewritten from memory.
	rs.present = false
	if _, err := ensureAll(spec(), c, fs, rs); err != nil {
		t.Fatal(err)
	}
	if !rs.present || !sameRoster(rs.labels, rosterLabels(spec())) {
		t.Fatalf("missing roster must be rewritten from memory, got %v", rs.labels)
	}
}

// noSleep is a no-op sleep so robustReload's retry path is exercised without real
// time in unit tests.
func noSleep(time.Duration) {}

// TestRobustReloadRetriesTransientBootstrap (issue #102-a): on a LIVE mesh, an
// immediate bootstrap after bootout can return EIO ("already loaded") while
// launchd finishes its async teardown. robustReload re-issues bootout + retries
// and succeeds within reloadAttempts — a single bootout+bootstrap would have
// failed (the 2/3 mesh fault).
func TestRobustReloadRetriesTransientBootstrap(t *testing.T) {
	c := newFakeCtl()
	label := "com.vendor.alpha"
	pp := "/p/" + label + ".plist"
	c.bootstrapFailN = map[string]int{pp: reloadAttempts - 1} // fail all but the last try

	if err := robustReload(c, label, pp, noSleep); err != nil {
		t.Fatalf("robustReload should succeed after transient failures, got %v", err)
	}
	if !c.loaded(label) {
		t.Fatalf("label must be loaded after a successful robustReload")
	}
	// bootout is idempotent and re-issued each attempt: reloadAttempts bootouts.
	if len(c.bouts) != reloadAttempts {
		t.Fatalf("want %d bootouts (one per attempt), got %d (%v)", reloadAttempts, len(c.bouts), c.bouts)
	}
}

// TestRobustReloadGivesUpAfterMaxAttempts: a bootstrap that NEVER succeeds returns
// the underlying error after reloadAttempts tries (does not loop forever).
func TestRobustReloadGivesUpAfterMaxAttempts(t *testing.T) {
	c := newFakeCtl()
	label := "com.vendor.bravo"
	pp := "/p/" + label + ".plist"
	c.bootstrapFailOn = pp // always fails

	if err := robustReload(c, label, pp, noSleep); err == nil {
		t.Fatal("robustReload must surface the error when every attempt fails")
	}
	if c.loaded(label) {
		t.Fatal("label must NOT be loaded when every bootstrap failed")
	}
}

// TestReinstallExceptSelfNeverBootstrapsSelf (issue #102-a/b): the re-materialize
// path must reload every mesh label EXCEPT the caller's own, and boot self OUT
// (no bootstrap) — so no process ever restarts its OWN executing label mid-install
// (the self-SIGTERM that released the lock and let a sibling double-place). Self
// ends genuinely !loaded; the next EnsureAll re-bootstraps it.
func TestReinstallExceptSelfNeverBootstrapsSelf(t *testing.T) {
	s := spec()
	// Pre-load all three (simulate the running mesh at the OLD path).
	c, fs, rs := newFakeCtl(), newFakeFS(), &fakeRoster{}
	if err := installAll(s, c, fs, rs); err != nil {
		t.Fatal(err)
	}
	selfRole := RoleA
	selfLabel := s.Label(selfRole)

	// Re-materialize at a NEW binary path (roster/labels unchanged).
	ns := s
	ns.SelfPath = "/wd/fresh-binary"
	c.boots = nil // reset to observe only the reinstall's bootstraps
	if err := reinstallExceptSelf(ns, c, fs, rs, selfLabel); err != nil {
		t.Fatalf("reinstallExceptSelf: %v", err)
	}

	// Self must NOT have been bootstrapped, and must be genuinely !loaded now.
	for _, pp := range c.boots {
		if labelFromPath(pp) == selfLabel {
			t.Fatalf("self label %q must NEVER be bootstrapped by reinstallExceptSelf", selfLabel)
		}
	}
	if c.loaded(selfLabel) {
		t.Fatalf("self label %q must be booted OUT (!loaded) after reinstallExceptSelf", selfLabel)
	}
	// The two non-self labels ARE reloaded and loaded.
	for _, r := range []Role{RoleB, RoleEnsure} {
		if !c.loaded(s.Label(r)) {
			t.Fatalf("non-self role %s must be reloaded by reinstallExceptSelf", r)
		}
	}
	// All three plists were (re)written at the new path (the roster + plists agree).
	if len(fs.files) != 3 {
		t.Fatalf("want 3 plists written at the new path, got %d", len(fs.files))
	}
	// The next EnsureAll re-bootstraps self onto the new binary (the mesh converges).
	rec, err := ensureAll(ns, c, fs, rs)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec) != 1 || rec[0] != selfRole {
		t.Fatalf("EnsureAll must re-bootstrap ONLY self, got %v", rec)
	}
	if !c.loaded(selfLabel) {
		t.Fatalf("self must be loaded again after the next EnsureAll")
	}
}

func TestUninstallAllRemovesEverything(t *testing.T) {
	c, fs, rs := newFakeCtl(), newFakeFS(), &fakeRoster{}
	_ = installAll(spec(), c, fs, rs)
	if err := uninstallAll(Spec{}, c, fs, rs); err != nil {
		t.Fatal(err)
	}
	for _, r := range AllRoles {
		if c.loaded((Spec{}).Label(r)) {
			t.Fatalf("role %s still loaded after uninstall", r)
		}
	}
	if len(fs.files) != 0 {
		t.Fatalf("plists not removed: %v", fs.files)
	}
	if rs.present {
		t.Fatal("uninstall must remove the masked roster file")
	}
}
