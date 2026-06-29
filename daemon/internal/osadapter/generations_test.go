//go:build darwin

package osadapter

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// writeGeneration writes the three mesh plists for one generation into laDir,
// with the disguised binary living inside its own workdir (mirrors
// relocate.RelocateInto). Returns the binary path + workdir.
func writeGeneration(t *testing.T, home, laDir, tag string, roster []string) (binPath, wd string) {
	t.Helper()
	wd = filepath.Join(home, "Library", "Application Support", "."+tag)
	binPath = filepath.Join(wd, tag+".bin")
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("FAKE-DAEMON-"+tag), 0o755); err != nil {
		t.Fatal(err)
	}
	s := Spec{Mode: mode.User, SelfPath: binPath, Workdir: wd, Roster: roster}
	for _, r := range AllRoles {
		pp := filepath.Join(laDir, s.Label(r)+".plist")
		if err := os.WriteFile(pp, []byte(Plist(s, r)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return binPath, wd
}

func laDirUnderHome(t *testing.T) (home, laDir string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	laDir = filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(laDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, laDir
}

// TestDiscoverAllGenerationsGroupsByVerifiedBinary: two generations + one
// unrelated (non-verifying) plist. Discovery groups by verified binary path,
// yields exactly the two generations (3 plists each), and ignores the vendor
// plist entirely.
func TestDiscoverAllGenerationsGroupsByVerifiedBinary(t *testing.T) {
	home, laDir := laDirUnderHome(t)
	roster1 := []string{"com.apple.metadata.helper.1111", "com.google.keystone.daemon.1112", "org.mozilla.updater.agent.1113"}
	roster2 := []string{"com.docker.helper.2221", "us.zoom.ZoomDaemon.svc.2222", "io.tailscale.ipnextension.relay.2223"}
	bin1, wd1 := writeGeneration(t, home, laDir, "gen1", roster1)
	bin2, wd2 := writeGeneration(t, home, laDir, "gen2", roster2)

	// An unrelated vendor plist whose binary the verifier rejects.
	vendorBin := filepath.Join(home, "vendor", "thing")
	if err := os.MkdirAll(filepath.Dir(vendorBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vendorBin, []byte("VENDOR"), 0o755); err != nil {
		t.Fatal(err)
	}
	vs := Spec{Mode: mode.User, SelfPath: vendorBin, Workdir: filepath.Dir(vendorBin),
		Roster: []string{"com.vendor.a", "com.vendor.b", "com.vendor.c"}}
	for _, r := range AllRoles {
		pp := filepath.Join(laDir, vs.Label(r)+".plist")
		if err := os.WriteFile(pp, []byte(Plist(vs, r)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Verifier accepts ONLY the two real generation binaries.
	verify := Verifier(func(p string) (bool, error) { return p == bin1 || p == bin2, nil })

	gens, err := DiscoverAllGenerations(mode.User, verify)
	if err != nil {
		t.Fatalf("DiscoverAllGenerations: %v", err)
	}
	if len(gens) != 2 {
		t.Fatalf("want 2 generations, got %d: %+v", len(gens), gens)
	}
	byBin := map[string]Generation{}
	for _, g := range gens {
		byBin[g.BinaryPath] = g
	}
	for bin, wd := range map[string]string{bin1: wd1, bin2: wd2} {
		g, ok := byBin[bin]
		if !ok {
			t.Fatalf("generation for %q missing", bin)
		}
		if g.Workdir != wd {
			t.Errorf("workdir = %q, want %q", g.Workdir, wd)
		}
		if len(g.Labels) != 3 || len(g.PlistPaths) != 3 {
			t.Errorf("generation %q: want 3 labels/plists, got %d/%d", bin, len(g.Labels), len(g.PlistPaths))
		}
	}
}

// TestDiscoverAllGenerationsExcludesNonMesh: a verified binary whose only plist
// is the ensure role (`ensure` — no --mesh) is NOT a real mesh generation and
// must be dropped. This pins the regression that a non-mesh plist is never
// retired.
func TestDiscoverAllGenerationsExcludesNonMesh(t *testing.T) {
	home, laDir := laDirUnderHome(t)
	wd := filepath.Join(home, "Library", "Application Support", ".ensureonly")
	binPath := filepath.Join(wd, "ensureonly.bin")
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("FAKE-DAEMON"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := Spec{Mode: mode.User, SelfPath: binPath, Workdir: wd,
		Roster: []string{"com.a.x", "com.b.y", "com.c.z"}}
	// Only the ensure plist (no --mesh) is present.
	pp := filepath.Join(laDir, s.Label(RoleEnsure)+".plist")
	if err := os.WriteFile(pp, []byte(Plist(s, RoleEnsure)), 0o644); err != nil {
		t.Fatal(err)
	}

	verify := Verifier(func(p string) (bool, error) { return p == binPath, nil })
	gens, err := DiscoverAllGenerations(mode.User, verify)
	if err != nil {
		t.Fatalf("DiscoverAllGenerations: %v", err)
	}
	if len(gens) != 0 {
		t.Fatalf("a verified-but-non-mesh generation must be excluded, got %+v", gens)
	}
}

// TestDiscoverAllGenerationsNoLaunchDir: a missing LaunchAgents dir yields zero
// generations and no error.
func TestDiscoverAllGenerationsNoLaunchDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	gens, err := DiscoverAllGenerations(mode.User, Verifier(func(string) (bool, error) { return false, nil }))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(gens) != 0 {
		t.Fatalf("want zero generations, got %+v", gens)
	}
}

// fakeRetire records the side effects of retireGenerations.
type fakeRetire struct {
	bootedOut    []string
	removedPlist []string
	killed       []string
	removedAll   []string
}

// TestRetireGenerationsKeepsKeepRetiresOthers: the keep generation is never
// touched; every other generation is booted out + plists removed + binary
// killed, and its workdir is RemoveAll'd ONLY when path-sanity allows.
func TestRetireGenerationsKeepsKeepRetiresOthers(t *testing.T) {
	root := "/Library/Application Support"
	keepBin := filepath.Join(root, ".keep", "keep.bin")
	keepWorkdir := filepath.Dir(keepBin)

	gens := []Generation{
		{ // surviving generation — must be skipped entirely
			BinaryPath: keepBin, Workdir: keepWorkdir,
			Labels:     []string{"k1", "k2", "k3"},
			PlistPaths: []string{"/p/k1", "/p/k2", "/p/k3"},
		},
		{ // old generation with a SAFE workdir → fully retired incl. RemoveAll
			BinaryPath: filepath.Join(root, ".old1", "old1.bin"),
			Workdir:    filepath.Join(root, ".old1"),
			Labels:     []string{"o1a", "o1b", "o1c"},
			PlistPaths: []string{"/p/o1a", "/p/o1b", "/p/o1c"},
		},
		{ // old generation with an UNSAFE workdir (outside root) → no RemoveAll
			BinaryPath: "/somewhere/else/old2.bin",
			Workdir:    "/somewhere/else",
			Labels:     []string{"o2a"},
			PlistPaths: []string{"/p/o2a"},
		},
	}

	f := &fakeRetire{}
	n := retireGenerations(gens, keepBin, root,
		func(l string) error { f.bootedOut = append(f.bootedOut, l); return nil },
		func(p string) error { f.removedPlist = append(f.removedPlist, p); return nil },
		func(b string) { f.killed = append(f.killed, b) },
		func(d string) error { f.removedAll = append(f.removedAll, d); return nil },
	)

	if n != 2 {
		t.Fatalf("retired count = %d, want 2", n)
	}
	// Keep generation untouched.
	for _, l := range f.bootedOut {
		if l == "k1" || l == "k2" || l == "k3" {
			t.Fatalf("keep generation label %q must never be booted out", l)
		}
	}
	if contains(f.removedAll, keepWorkdir) {
		t.Fatalf("keep workdir must never be RemoveAll'd")
	}
	// Old1 fully retired including its (safe) workdir.
	if !contains(f.removedAll, filepath.Join(root, ".old1")) {
		t.Fatalf("safe old1 workdir must be RemoveAll'd, got %v", f.removedAll)
	}
	// Old2's unsafe workdir must NOT be removed, but it is still booted out.
	if contains(f.removedAll, "/somewhere/else") {
		t.Fatalf("unsafe workdir must NOT be RemoveAll'd, got %v", f.removedAll)
	}
	if !contains(f.bootedOut, "o2a") {
		t.Fatalf("old2 label must still be booted out, got %v", f.bootedOut)
	}
	if !contains(f.killed, "/somewhere/else/old2.bin") {
		t.Fatalf("old2 binary must be killed, got %v", f.killed)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestParsePlistBinaryFormat: a BINARY-format plist (the real-world hardening
// case) is converted via plutil and parsed correctly — label, binary path, and
// argv (including --mesh) are all recovered.
func TestParsePlistBinaryFormat(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "com.apple.metadata.helper.bbbb")
	s := Spec{Mode: mode.User, SelfPath: binPath, Workdir: dir,
		Roster: []string{"com.apple.metadata.helper.1", "com.google.keystone.daemon.2", "org.mozilla.updater.agent.3"}}
	plistPath := filepath.Join(dir, "gen.plist")
	if err := os.WriteFile(plistPath, []byte(Plist(s, RoleA)), 0o644); err != nil {
		t.Fatal(err)
	}
	// Convert the on-disk plist to BINARY format in place.
	if out, err := exec.Command("plutil", "-convert", "binary1", plistPath).CombinedOutput(); err != nil {
		t.Skipf("plutil unavailable or failed (%v): %s", err, out)
	}
	// Sanity: it really is binary now (raw scan would fail).
	raw, _ := os.ReadFile(plistPath)
	if strings.Contains(string(raw), "<plist") {
		t.Fatal("expected a binary plist, but it still looks like XML")
	}

	label, bin, argv := parsePlist(plistPath)
	if label != s.Label(RoleA) {
		t.Errorf("label = %q, want %q", label, s.Label(RoleA))
	}
	if bin != binPath {
		t.Errorf("bin = %q, want %q", bin, binPath)
	}
	if !hasMeshFlag(argv) {
		t.Errorf("argv must carry --mesh, got %v", argv)
	}
}
