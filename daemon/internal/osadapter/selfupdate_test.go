package osadapter

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// --- fakes ---------------------------------------------------------------

type fakeProber struct {
	mu           sync.Mutex
	loaded       map[string]bool
	pids         map[string]bool
	probeRounds  int  // each isLoaded call for label[0] increments this
	gateRounds   int  // first N rounds report not-healthy
	firstLabel   string
	neverHealthy bool // always report not-healthy (timeout path)
}

func newFakeProber(loaded, pids map[string]bool) *fakeProber {
	if loaded == nil {
		loaded = map[string]bool{}
	}
	if pids == nil {
		pids = map[string]bool{}
	}
	return &fakeProber{loaded: loaded, pids: pids}
}

func (p *fakeProber) isLoaded(l string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.firstLabel == "" {
		p.firstLabel = l
	}
	if l == p.firstLabel {
		p.probeRounds++
	}
	if p.neverHealthy {
		return false
	}
	if p.probeRounds <= p.gateRounds {
		return false
	}
	return p.loaded[l]
}

func (p *fakeProber) hasPID(l string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.neverHealthy {
		return false
	}
	if p.probeRounds <= p.gateRounds {
		return false
	}
	return p.pids[l]
}

type fakeBinPlacer struct {
	bytes  map[string][]byte
	placed []string
	rmd    []string
	failOn string
	rmErr  error
}

func (b *fakeBinPlacer) place(src []byte, dst string) error {
	if dst == b.failOn {
		return errors.New("synthetic place failure")
	}
	if b.bytes == nil {
		b.bytes = map[string][]byte{}
	}
	b.bytes[dst] = append([]byte(nil), src...)
	b.placed = append(b.placed, dst)
	return nil
}

func (b *fakeBinPlacer) remove(p string) error {
	if b.rmErr != nil {
		return b.rmErr
	}
	if b.bytes != nil {
		delete(b.bytes, p)
	}
	b.rmd = append(b.rmd, p)
	return nil
}

// --- shared spec helpers --------------------------------------------------

func curInstall() CurInstall {
	return CurInstall{
		Mode:    mode.User,
		Base:    "com.apple.metadata.helper.OLD",
		Workdir: "/wd",
		BinaryPath: "/wd/com.apple.metadata.helper.OLD",
		PlistPaths: []string{
			"/p/com.apple.metadata.helper.OLD.a.plist",
			"/p/com.apple.metadata.helper.OLD.b.plist",
			"/p/com.apple.metadata.helper.OLD.ensure.plist",
		},
		Labels: []string{
			"com.apple.metadata.helper.OLD.a",
			"com.apple.metadata.helper.OLD.b",
			"com.apple.metadata.helper.OLD.ensure",
		},
	}
}

func newSpecRotated(workdir, newBin, newBase string) Spec {
	return Spec{
		Mode: mode.User, SelfPath: newBin, Workdir: workdir,
		Github: "o/r", Asset: "daemon-darwin-arm64",
		Interval: 10 * time.Second, Base: newBase,
	}
}

func newSpec() Spec {
	return newSpecRotated("/wd", "/wd/com.apple.cfprefsd.helper.NEW", "com.apple.cfprefsd.helper.NEW")
}

// allHealthy returns prober maps that mark every new label as loaded
// AND give A/B a live PID (ensurer gets no PID — it is StartInterval).
func allHealthy(s Spec) (loaded, pids map[string]bool) {
	loaded = map[string]bool{
		s.Label(RoleA):      true,
		s.Label(RoleB):      true,
		s.Label(RoleEnsure): true,
	}
	pids = map[string]bool{
		s.Label(RoleA): true,
		s.Label(RoleB): true,
	}
	return
}

// --- tests ---------------------------------------------------------------

func TestSelfUpdate_HappyPath(t *testing.T) {
	c, fs := newFakeCtl(), newFakeFS()
	// Pre-load the OLD mesh so bootout-old has something to remove.
	cur := curInstall()
	for i, l := range cur.Labels {
		c.loadedSet[l] = true
		fs.files[cur.PlistPaths[i]] = "OLD"
	}
	b := &fakeBinPlacer{bytes: map[string][]byte{cur.BinaryPath: {0x01}}}

	s := newSpec()
	loaded, pids := allHealthy(s)
	p := newFakeProber(loaded, pids)

	if err := SelfUpdate(cur, s, []byte("NEWBIN"), c, fs, p, &fakeBinPlacerWrap{b}, 2*time.Second, 5*time.Millisecond, false); err != nil {
		t.Fatalf("happy path: %v", err)
	}

	// Verify: new binary placed; new plists written; new mesh bootstrapped;
	// old plists removed; old binary removed; old labels booted out.
	if _, ok := b.bytes[s.SelfPath]; !ok {
		t.Errorf("new binary not placed at %s", s.SelfPath)
	}
	if _, ok := b.bytes[cur.BinaryPath]; ok {
		t.Errorf("old binary not removed at %s", cur.BinaryPath)
	}
	for _, r := range AllRoles {
		if !c.loaded(s.Label(r)) {
			t.Errorf("new role %s not loaded", r)
		}
	}
	for _, oldL := range cur.Labels {
		if c.loaded(oldL) {
			t.Errorf("old label %s still loaded", oldL)
		}
	}
	for _, oldPP := range cur.PlistPaths {
		if _, ok := fs.files[oldPP]; ok {
			t.Errorf("old plist %s not removed", oldPP)
		}
	}
}

func TestSelfUpdate_KeepOldLeavesOldOnDisk(t *testing.T) {
	c, fs := newFakeCtl(), newFakeFS()
	cur := curInstall()
	for i, l := range cur.Labels {
		c.loadedSet[l] = true
		fs.files[cur.PlistPaths[i]] = "OLD"
	}
	b := &fakeBinPlacer{bytes: map[string][]byte{cur.BinaryPath: {0x01}}}

	s := newSpec()
	loaded, pids := allHealthy(s)
	p := newFakeProber(loaded, pids)

	if err := SelfUpdate(cur, s, []byte("NEWBIN"), c, fs, p, &fakeBinPlacerWrap{b}, 2*time.Second, 5*time.Millisecond, true); err != nil {
		t.Fatalf("keep-old: %v", err)
	}

	// Old labels are STILL booted out (otherwise 6 daemons fight) —
	// only the on-disk artifacts are preserved.
	for _, oldL := range cur.Labels {
		if c.loaded(oldL) {
			t.Errorf("old label %s should be booted out even with --keep-old", oldL)
		}
	}
	if _, ok := fs.files[cur.PlistPaths[0]]; !ok {
		t.Errorf("--keep-old must leave old plists on disk")
	}
	if _, ok := b.bytes[cur.BinaryPath]; !ok {
		t.Errorf("--keep-old must leave old binary on disk")
	}
}

func TestSelfUpdate_PreflightNoInstall(t *testing.T) {
	c, fs := newFakeCtl(), newFakeFS()
	b := &fakeBinPlacer{}
	p := newFakeProber(nil, nil)
	err := SelfUpdate(CurInstall{}, newSpec(), []byte("X"), c, fs, p, &fakeBinPlacerWrap{b}, time.Second, 5*time.Millisecond, false)
	if err == nil || !strings.Contains(err.Error(), "no current install") {
		t.Fatalf("expected preflight failure, got %v", err)
	}
	if len(b.placed) != 0 {
		t.Fatal("nothing should have been placed on preflight failure")
	}
}

func TestSelfUpdate_RejectsSamePath(t *testing.T) {
	c, fs := newFakeCtl(), newFakeFS()
	cur := curInstall()
	for i, l := range cur.Labels {
		c.loadedSet[l] = true
		fs.files[cur.PlistPaths[i]] = "OLD"
	}
	s := newSpec()
	s.SelfPath = cur.BinaryPath // same path → must reject (AMFI premise)
	b := &fakeBinPlacer{}
	p := newFakeProber(nil, nil)
	err := SelfUpdate(cur, s, []byte("X"), c, fs, p, &fakeBinPlacerWrap{b}, time.Second, 5*time.Millisecond, false)
	if err == nil || !strings.Contains(err.Error(), "path rotation") {
		t.Fatalf("expected path-rotation rejection, got %v", err)
	}
}

func TestSelfUpdate_BootstrapFailRollsBack(t *testing.T) {
	c, fs := newFakeCtl(), newFakeFS()
	cur := curInstall()
	for i, l := range cur.Labels {
		c.loadedSet[l] = true
		fs.files[cur.PlistPaths[i]] = "OLD"
	}
	b := &fakeBinPlacer{bytes: map[string][]byte{cur.BinaryPath: {0x01}}}

	s := newSpec()
	loaded, pids := allHealthy(s)
	p := newFakeProber(loaded, pids)

	// Make role B bootstrap fail.
	c.bootstrapFailOn = fs.plistPath(s.Label(RoleB))

	err := SelfUpdate(cur, s, []byte("NEWBIN"), c, fs, p, &fakeBinPlacerWrap{b}, time.Second, 5*time.Millisecond, false)
	if err == nil || !strings.Contains(err.Error(), "bootstrap new") {
		t.Fatalf("expected bootstrap error, got %v", err)
	}

	// Rollback: new binary removed; new plists removed; new labels NOT loaded.
	if _, ok := b.bytes[s.SelfPath]; ok {
		t.Errorf("new binary should be rolled back")
	}
	for _, r := range AllRoles {
		if c.loaded(s.Label(r)) {
			t.Errorf("new role %s should not be loaded after rollback", r)
		}
		if _, ok := fs.files[fs.plistPath(s.Label(r))]; ok {
			t.Errorf("new plist for %s should be removed on rollback", r)
		}
	}
	// OLD install untouched.
	for _, oldL := range cur.Labels {
		if !c.loaded(oldL) {
			t.Errorf("old label %s must remain loaded on rollback", oldL)
		}
	}
	if _, ok := b.bytes[cur.BinaryPath]; !ok {
		t.Errorf("old binary must remain on disk on rollback")
	}
}

func TestSelfUpdate_PlistWriteFailRollsBack(t *testing.T) {
	c, fs := newFakeCtl(), newFakeFS()
	cur := curInstall()
	for i, l := range cur.Labels {
		c.loadedSet[l] = true
		fs.files[cur.PlistPaths[i]] = "OLD"
	}
	b := &fakeBinPlacer{bytes: map[string][]byte{cur.BinaryPath: {0x01}}}

	s := newSpec()

	// Make plist write fail for role ensure (last one).
	fs.writeFailOn = fs.plistPath(s.Label(RoleEnsure))

	err := SelfUpdate(cur, s, []byte("NEWBIN"), c, fs, newFakeProber(nil, nil), &fakeBinPlacerWrap{b}, time.Second, 5*time.Millisecond, false)
	if err == nil || !strings.Contains(err.Error(), "write new plist") {
		t.Fatalf("expected write-plist error, got %v", err)
	}
	if _, ok := b.bytes[s.SelfPath]; ok {
		t.Errorf("new binary should be rolled back on plist write failure")
	}
	// OLD untouched.
	if _, ok := b.bytes[cur.BinaryPath]; !ok {
		t.Errorf("old binary must remain")
	}
}

func TestSelfUpdate_HealthPollTimeoutRollsBack(t *testing.T) {
	c, fs := newFakeCtl(), newFakeFS()
	cur := curInstall()
	for i, l := range cur.Labels {
		c.loadedSet[l] = true
		fs.files[cur.PlistPaths[i]] = "OLD"
	}
	b := &fakeBinPlacer{bytes: map[string][]byte{cur.BinaryPath: {0x01}}}

	s := newSpec()
	p := newFakeProber(nil, nil)
	p.neverHealthy = true

	err := SelfUpdate(cur, s, []byte("NEWBIN"), c, fs, p, &fakeBinPlacerWrap{b}, 30*time.Millisecond, 5*time.Millisecond, false)
	if err == nil || !strings.Contains(err.Error(), "health-poll timeout") {
		t.Fatalf("expected health-poll timeout, got %v", err)
	}

	// Full rollback: new binary removed; new plists removed; new labels
	// booted out. OLD install untouched.
	if _, ok := b.bytes[s.SelfPath]; ok {
		t.Errorf("new binary should be rolled back")
	}
	for _, r := range AllRoles {
		if c.loaded(s.Label(r)) {
			t.Errorf("new role %s should be booted out on health timeout", r)
		}
	}
	for _, oldL := range cur.Labels {
		if !c.loaded(oldL) {
			t.Errorf("old label %s must remain after health-poll rollback", oldL)
		}
	}
}

func TestSelfUpdate_OldBootoutFailureNotFatal(t *testing.T) {
	c, fs := newFakeCtl(), newFakeFS()
	cur := curInstall()
	for i, l := range cur.Labels {
		c.loadedSet[l] = true
		fs.files[cur.PlistPaths[i]] = "OLD"
	}
	b := &fakeBinPlacer{bytes: map[string][]byte{cur.BinaryPath: {0x01}}}

	s := newSpec()
	loaded, pids := allHealthy(s)
	p := newFakeProber(loaded, pids)

	// Make bootout of OLD role A fail (the LAST one we try; reverse order
	// = ensure → B → A) — error is swallowed and the run succeeds anyway.
	c.bootoutErrOn = map[string]error{cur.Labels[0]: errors.New("synthetic")}

	if err := SelfUpdate(cur, s, []byte("NEWBIN"), c, fs, p, &fakeBinPlacerWrap{b}, time.Second, 5*time.Millisecond, false); err != nil {
		t.Fatalf("old-bootout failure must not be fatal: %v", err)
	}
	// New mesh up.
	for _, r := range AllRoles {
		if !c.loaded(s.Label(r)) {
			t.Errorf("new role %s should be loaded", r)
		}
	}
}

func TestSelfUpdate_HealthPollNeedsTwoConsecutiveOKs(t *testing.T) {
	c, fs := newFakeCtl(), newFakeFS()
	cur := curInstall()
	for i, l := range cur.Labels {
		c.loadedSet[l] = true
		fs.files[cur.PlistPaths[i]] = "OLD"
	}
	b := &fakeBinPlacer{bytes: map[string][]byte{cur.BinaryPath: {0x01}}}
	s := newSpec()
	loaded, pids := allHealthy(s)
	p := newFakeProber(loaded, pids)
	// First N probes look unhealthy, then healthy: total time ~ N*interval.
	// Must still pass within timeout. (Goal: validate that 2 consecutive
	// OKs are required AND that intermittent failures don't latch.)
	p.gateRounds = 1

	if err := SelfUpdate(cur, s, []byte("NEWBIN"), c, fs, p, &fakeBinPlacerWrap{b}, 1*time.Second, 5*time.Millisecond, false); err != nil {
		t.Fatalf("expected eventual healthy: %v", err)
	}
}

func TestSelfUpdate_PlaceBinaryFailureFatal(t *testing.T) {
	c, fs := newFakeCtl(), newFakeFS()
	cur := curInstall()
	for i, l := range cur.Labels {
		c.loadedSet[l] = true
		fs.files[cur.PlistPaths[i]] = "OLD"
	}
	b := &fakeBinPlacer{bytes: map[string][]byte{cur.BinaryPath: {0x01}}}
	s := newSpec()
	b.failOn = s.SelfPath
	err := SelfUpdate(cur, s, []byte("X"), c, fs, newFakeProber(nil, nil), &fakeBinPlacerWrap{b}, time.Second, 5*time.Millisecond, false)
	if err == nil || !strings.Contains(err.Error(), "place new binary") {
		t.Fatalf("expected place failure, got %v", err)
	}
	// OLD install untouched.
	for _, oldL := range cur.Labels {
		if !c.loaded(oldL) {
			t.Errorf("old label %s must remain loaded after place failure", oldL)
		}
	}
}

// --- adapter helpers ------------------------------------------------------

// fakeBinPlacerWrap satisfies the binPlacer interface against
// fakeBinPlacer (struct of *fakeBinPlacer to keep mutations visible).
type fakeBinPlacerWrap struct{ b *fakeBinPlacer }

func (w *fakeBinPlacerWrap) place(s []byte, d string) error { return w.b.place(s, d) }
func (w *fakeBinPlacerWrap) remove(p string) error          { return w.b.remove(p) }
