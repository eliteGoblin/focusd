package osadapter

import (
	"strings"
	"testing"
	"time"
)

type fakeCtl struct {
	loadedSet map[string]bool
	boots     []string
	bouts     []string
}

func newFakeCtl() *fakeCtl { return &fakeCtl{loadedSet: map[string]bool{}} }

func (f *fakeCtl) loaded(l string) bool { return f.loadedSet[l] }
func (f *fakeCtl) bootstrap(pp string) error {
	f.boots = append(f.boots, pp)
	f.loadedSet[labelFromPath(pp)] = true
	return nil
}
func (f *fakeCtl) bootout(l string) error {
	f.bouts = append(f.bouts, l)
	delete(f.loadedSet, l)
	return nil
}

type fakeFS struct{ files map[string]string }

func newFakeFS() *fakeFS                    { return &fakeFS{files: map[string]string{}} }
func (f *fakeFS) plistPath(l string) string { return "/p/" + l + ".plist" }
func (f *fakeFS) write(p, c string) error   { f.files[p] = c; return nil }
func (f *fakeFS) remove(p string) error     { delete(f.files, p); return nil }
func labelFromPath(p string) string {
	p = strings.TrimPrefix(p, "/p/")
	return strings.TrimSuffix(p, ".plist")
}

func spec() Spec {
	return Spec{SelfPath: "/d/daemon", Workdir: "/wd", Github: "o/r",
		Asset: "platform-darwin-arm64", Interval: 10 * time.Second}
}

func TestInstallAllWritesAndLoadsThree(t *testing.T) {
	c, fs := newFakeCtl(), newFakeFS()
	if err := installAll(spec(), c, fs); err != nil {
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
}

func TestEnsureAllRecreatesOnlyMissing(t *testing.T) {
	c, fs := newFakeCtl(), newFakeFS()
	_ = installAll(spec(), c, fs)
	// Simulate the user killing role A's entry.
	_ = c.bootout(spec().Label(RoleA))
	delete(fs.files, fs.plistPath(spec().Label(RoleA)))

	rec, err := ensureAll(spec(), c, fs)
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
	rec2, _ := ensureAll(spec(), c, fs)
	if len(rec2) != 0 {
		t.Fatalf("ensure must be idempotent, recreated %v", rec2)
	}
}

func TestUninstallAllRemovesEverything(t *testing.T) {
	c, fs := newFakeCtl(), newFakeFS()
	_ = installAll(spec(), c, fs)
	if err := uninstallAll(Spec{}, c, fs); err != nil {
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
}
