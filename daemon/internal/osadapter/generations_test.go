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

// writeDeadGeneration writes the given roles' mesh plists for a generation
// whose binary is DELETED (never created) — the zombie left by a workdir-delete
// recovery cycle. Returns the dangling binary path + workdir.
func writeDeadGeneration(t *testing.T, home, laDir, tag string, roster []string, roles ...Role) (binPath, wd string) {
	t.Helper()
	wd = filepath.Join(home, "Library", "Application Support", "."+tag)
	binPath = filepath.Join(wd, tag+".bin")
	s := Spec{Mode: mode.User, SelfPath: binPath, Workdir: wd, Roster: roster}
	for _, r := range roles {
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

	gens, dead, err := DiscoverAllGenerations(mode.User, verify)
	if err != nil {
		t.Fatalf("DiscoverAllGenerations: %v", err)
	}
	if len(dead) != 0 {
		t.Fatalf("present binaries must not be dead generations, got %+v", dead)
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
	gens, dead, err := DiscoverAllGenerations(mode.User, verify)
	if err != nil {
		t.Fatalf("DiscoverAllGenerations: %v", err)
	}
	if len(gens) != 0 {
		t.Fatalf("a verified-but-non-mesh generation must be excluded, got %+v", gens)
	}
	if len(dead) != 0 {
		t.Fatalf("a present-binary generation must never be dead, got %+v", dead)
	}
}

// TestDiscoverAllGenerationsNoLaunchDir: a missing LaunchAgents dir yields zero
// generations and no error.
func TestDiscoverAllGenerationsNoLaunchDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	gens, dead, err := DiscoverAllGenerations(mode.User, Verifier(func(string) (bool, error) { return false, nil }))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(gens) != 0 {
		t.Fatalf("want zero generations, got %+v", gens)
	}
	if len(dead) != 0 {
		t.Fatalf("want zero dead generations, got %+v", dead)
	}
}

// readFileVerifier mimics sig.VerifyFile's classification for tests: a deleted
// binary yields a wrapped ENOENT error (the "dead generation" path), a present
// "VENDOR" file fails the signature (ok=false), and any other present file is
// accepted. errors.Is(err, fs.ErrNotExist) holds because os.ReadFile wraps the
// real syscall error.
func readFileVerifier(p string) (bool, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return false, err // deleted binary → ENOENT → dead generation
	}
	return string(data) != "VENDOR", nil
}

// TestDiscoverAllGenerationsRecordsDeadBinary: a generation whose binary was
// DELETED but whose plists carry the mesh worker marker is returned as a DEAD
// generation (not silently dropped). A vendor plist (binary present, fails sig)
// is excluded. A non-mesh dead plist (deleted binary, ensure-only, no marker)
// is NOT treated as ours.
func TestDiscoverAllGenerationsRecordsDeadBinary(t *testing.T) {
	home, laDir := laDirUnderHome(t)

	// A live, present generation (control: must stay live, never dead).
	liveRoster := []string{"com.apple.metadata.helper.1", "com.google.keystone.daemon.2", "org.mozilla.updater.agent.3"}
	liveBin, _ := writeGeneration(t, home, laDir, "live", liveRoster)

	// A DEAD full-mesh generation: binary deleted, all three plists present
	// (workers carry the mesh marker → meshSeen true). The ensure plist has no
	// marker but shares the dangling bin path, so it is swept in.
	deadRoster := []string{"com.docker.helper.4", "us.zoom.ZoomDaemon.svc.5", "io.tailscale.ipnextension.relay.6"}
	deadBin, deadWd := writeDeadGeneration(t, home, laDir, "dead", deadRoster, AllRoles...)

	// An ENSURE-ONLY DEAD orphan: binary deleted, ONLY the ensure plist left (its
	// workers already swept). Issue #102-c: this MUST now be recognised as ours (a
	// dead ensurer carries the focusd-specific APP_LAUNCH_CONTEXT="ensure" marker),
	// so it is swept rather than stranded launchd-active with a missing binary.
	ensRoster := []string{"com.vendor.x.7", "com.vendor.y.8", "com.vendor.z.9"}
	ensBin, _ := writeDeadGeneration(t, home, laDir, "ensonly", ensRoster, RoleEnsure)

	// A vendor plist whose binary EXISTS but fails the signature.
	vendorWd := filepath.Join(home, "Library", "Application Support", ".vendor")
	vendorBin := filepath.Join(vendorWd, "vendor.bin")
	if err := os.MkdirAll(vendorWd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vendorBin, []byte("VENDOR"), 0o755); err != nil {
		t.Fatal(err)
	}
	vs := Spec{Mode: mode.User, SelfPath: vendorBin, Workdir: vendorWd,
		Roster: []string{"com.v.a.a", "com.v.b.b", "com.v.c.c"}}
	for _, r := range AllRoles {
		pp := filepath.Join(laDir, vs.Label(r)+".plist")
		if err := os.WriteFile(pp, []byte(Plist(vs, r)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	gens, dead, err := DiscoverAllGenerations(mode.User, Verifier(readFileVerifier))
	if err != nil {
		t.Fatalf("DiscoverAllGenerations: %v", err)
	}

	// Only the live generation is live.
	if len(gens) != 1 || gens[0].BinaryPath != liveBin {
		t.Fatalf("want exactly the live generation, got %+v", gens)
	}
	// TWO dead generations now: the full-mesh zombie AND the ensure-only orphan
	// (#102-c). The vendor plist (present binary, fails sig) is still excluded.
	if len(dead) != 2 {
		t.Fatalf("want exactly 2 dead generations (full-mesh + ensure-only), got %d: %+v", len(dead), dead)
	}
	byBin := map[string]DeadGeneration{}
	for _, d := range dead {
		byBin[d.BinaryPath] = d
	}
	full, ok := byBin[deadBin]
	if !ok {
		t.Fatalf("full-mesh dead generation %q missing from %+v", deadBin, dead)
	}
	if full.Workdir != deadWd {
		t.Errorf("dead workdir = %q, want %q", full.Workdir, deadWd)
	}
	if len(full.Labels) != 3 || len(full.PlistPaths) != 3 {
		t.Errorf("full-mesh dead generation: want 3 labels/plists (incl. swept-in ensure), got %d/%d", len(full.Labels), len(full.PlistPaths))
	}
	ens, ok := byBin[ensBin]
	if !ok {
		t.Fatalf("ensure-only dead orphan %q must now be recognised (#102-c), got %+v", ensBin, dead)
	}
	if len(ens.Labels) != 1 {
		t.Errorf("ensure-only dead orphan: want 1 label, got %d", len(ens.Labels))
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
// killed, and its workdir is RemoveAll'd ONLY when path-sanity allows. Uses
// real on-disk dirs because safeToRemoveWorkdir now resolves symlinks (a
// non-existent workdir is refused).
func TestRetireGenerationsKeepsKeepRetiresOthers(t *testing.T) {
	mkdir := func(p string) string {
		t.Helper()
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	root := t.TempDir()
	keepWorkdir := mkdir(filepath.Join(root, ".keep"))
	keepBin := filepath.Join(keepWorkdir, "keep.bin")
	old1Workdir := mkdir(filepath.Join(root, ".old1")) // SAFE: real, under root
	old2Workdir := t.TempDir()                         // UNSAFE: real, outside root

	gens := []Generation{
		{ // surviving generation — must be skipped entirely
			BinaryPath: keepBin, Workdir: keepWorkdir,
			Labels:     []string{"k1", "k2", "k3"},
			PlistPaths: []string{"/p/k1", "/p/k2", "/p/k3"},
		},
		{ // old generation with a SAFE workdir → fully retired incl. RemoveAll
			BinaryPath: filepath.Join(old1Workdir, "old1.bin"),
			Workdir:    old1Workdir,
			Labels:     []string{"o1a", "o1b", "o1c"},
			PlistPaths: []string{"/p/o1a", "/p/o1b", "/p/o1c"},
		},
		{ // old generation with an UNSAFE workdir (outside root) → no RemoveAll
			BinaryPath: filepath.Join(old2Workdir, "old2.bin"),
			Workdir:    old2Workdir,
			Labels:     []string{"o2a"},
			PlistPaths: []string{"/p/o2a"},
		},
	}

	f := &fakeRetire{}
	n := retireGenerations(gens, nil, keepBin, root,
		func(l string) error { f.bootedOut = append(f.bootedOut, l); return nil },
		func(p string) error { f.removedPlist = append(f.removedPlist, p); return nil },
		func(b string) { f.killed = append(f.killed, b) },
		func(string) {}, // killGenPlatform: no-op (platform-kill covered by FEATURE 25 tests)
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
	if !contains(f.removedAll, old1Workdir) {
		t.Fatalf("safe old1 workdir must be RemoveAll'd, got %v", f.removedAll)
	}
	// Old2's unsafe workdir must NOT be removed, but it is still booted out.
	if contains(f.removedAll, old2Workdir) {
		t.Fatalf("unsafe workdir must NOT be RemoveAll'd, got %v", f.removedAll)
	}
	if !contains(f.bootedOut, "o2a") {
		t.Fatalf("old2 label must still be booted out, got %v", f.bootedOut)
	}
	if !contains(f.killed, filepath.Join(old2Workdir, "old2.bin")) {
		t.Fatalf("old2 binary must be killed, got %v", f.killed)
	}
}

// TestRetireGenerationsNoopOnEmptyKeep: an empty keepBinaryPath is a bug, not a
// request to wipe everything. With a real generation present and no keep target,
// retireGenerations must retire NOTHING and never touch launchd/FS — otherwise
// the empty keep would make every generation "other" and tear the mesh down
// (ROOT bootout + os.RemoveAll blast radius).
func TestRetireGenerationsNoopOnEmptyKeep(t *testing.T) {
	root := t.TempDir()
	gens := []Generation{
		{
			BinaryPath: filepath.Join(root, ".only", "only.bin"),
			Workdir:    filepath.Join(root, ".only"),
			Labels:     []string{"a", "b", "c"},
			PlistPaths: []string{"/p/a", "/p/b", "/p/c"},
		},
	}
	f := &fakeRetire{}
	n := retireGenerations(gens, nil, "", root,
		func(l string) error { f.bootedOut = append(f.bootedOut, l); return nil },
		func(p string) error { f.removedPlist = append(f.removedPlist, p); return nil },
		func(b string) { f.killed = append(f.killed, b) },
		func(string) {}, // killGenPlatform: no-op (platform-kill covered by FEATURE 25 tests)
		func(d string) error { f.removedAll = append(f.removedAll, d); return nil },
	)
	if n != 0 {
		t.Fatalf("empty keep must retire 0, got %d", n)
	}
	if len(f.bootedOut) != 0 {
		t.Fatalf("empty keep must never bootout, got %v", f.bootedOut)
	}
	if len(f.removedPlist) != 0 || len(f.killed) != 0 || len(f.removedAll) != 0 {
		t.Fatalf("empty keep must have zero side effects, got plist=%v killed=%v removedAll=%v",
			f.removedPlist, f.killed, f.removedAll)
	}
}

// TestRetireGenerationsRetiresDeadZombies: dead generations are booted out,
// their plists removed, the dangling binary pkill'd, and their workdir
// RemoveAll'd ONLY when path-sanity allows — counted in the retired total. The
// keep generation is never touched.
func TestRetireGenerationsRetiresDeadZombies(t *testing.T) {
	mkdir := func(p string) string {
		t.Helper()
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	root := t.TempDir()
	keepWorkdir := mkdir(filepath.Join(root, ".keep"))
	keepBin := filepath.Join(keepWorkdir, "keep.bin")

	// Dead zombie with a SAFE workdir (still on disk, under root) → full retire
	// including RemoveAll. A real zombie's workdir is often already gone, in
	// which case safeToRemoveWorkdir refuses (EvalSymlinks fails) — but the
	// bootout/rm/pkill still run; we keep it present here to exercise RemoveAll.
	deadSafeWd := mkdir(filepath.Join(root, ".deadsafe"))
	// Dead zombie with an UNSAFE workdir (outside root) → no RemoveAll, still torn down.
	deadUnsafeWd := t.TempDir()

	gens := []Generation{{
		BinaryPath: keepBin, Workdir: keepWorkdir,
		Labels:     []string{"k1", "k2", "k3"},
		PlistPaths: []string{"/p/k1", "/p/k2", "/p/k3"},
	}}
	dead := []DeadGeneration{
		{
			BinaryPath: filepath.Join(deadSafeWd, "deadsafe.bin"),
			Workdir:    deadSafeWd,
			Labels:     []string{"d1a", "d1b", "d1c"},
			PlistPaths: []string{"/p/d1a", "/p/d1b", "/p/d1c"},
		},
		{
			BinaryPath: filepath.Join(deadUnsafeWd, "deadunsafe.bin"),
			Workdir:    deadUnsafeWd,
			Labels:     []string{"d2a"},
			PlistPaths: []string{"/p/d2a"},
		},
	}

	f := &fakeRetire{}
	n := retireGenerations(gens, dead, keepBin, root,
		func(l string) error { f.bootedOut = append(f.bootedOut, l); return nil },
		func(p string) error { f.removedPlist = append(f.removedPlist, p); return nil },
		func(b string) { f.killed = append(f.killed, b) },
		func(string) {}, // killGenPlatform: no-op (platform-kill covered by FEATURE 25 tests)
		func(d string) error { f.removedAll = append(f.removedAll, d); return nil },
	)

	// Only keep is live (skipped); both dead zombies retired.
	if n != 2 {
		t.Fatalf("retired count = %d, want 2 (both dead zombies)", n)
	}
	// Keep generation untouched.
	if contains(f.bootedOut, "k1") || contains(f.removedAll, keepWorkdir) {
		t.Fatalf("keep generation must never be retired, bootedOut=%v removedAll=%v", f.bootedOut, f.removedAll)
	}
	// Both dead zombies booted out + binaries killed.
	for _, lbl := range []string{"d1a", "d1b", "d1c", "d2a"} {
		if !contains(f.bootedOut, lbl) {
			t.Fatalf("dead label %q must be booted out, got %v", lbl, f.bootedOut)
		}
	}
	if !contains(f.killed, filepath.Join(deadSafeWd, "deadsafe.bin")) ||
		!contains(f.killed, filepath.Join(deadUnsafeWd, "deadunsafe.bin")) {
		t.Fatalf("dead binaries must be pkill'd, got %v", f.killed)
	}
	// Safe dead workdir removed; unsafe one NOT.
	if !contains(f.removedAll, deadSafeWd) {
		t.Fatalf("safe dead workdir must be RemoveAll'd, got %v", f.removedAll)
	}
	if contains(f.removedAll, deadUnsafeWd) {
		t.Fatalf("unsafe dead workdir must NOT be RemoveAll'd, got %v", f.removedAll)
	}
}

// TestRetireGenerationsSkipsPkillOnShortPath: a dead-generation plist whose
// ProgramArguments[0] is a short root-ish path (e.g. "/") must NOT expand into
// a broad `pkill -f /` that reaps unrelated processes. The generation is still
// booted out + plists removed, but killBin is never called for the short path.
func TestRetireGenerationsSkipsPkillOnShortPath(t *testing.T) {
	root := t.TempDir()
	keepWorkdir := filepath.Join(root, ".keep")
	if err := os.MkdirAll(keepWorkdir, 0o755); err != nil {
		t.Fatal(err)
	}
	keepBin := filepath.Join(keepWorkdir, "keep.bin")

	dead := []DeadGeneration{{
		BinaryPath: "/", // corrupt/dangling short path — must NOT be pkill'd
		Workdir:    "/", // unsafe by construction → no RemoveAll either
		Labels:     []string{"shortd"},
		PlistPaths: []string{"/p/shortd"},
	}}

	f := &fakeRetire{}
	n := retireGenerations(nil, dead, keepBin, root,
		func(l string) error { f.bootedOut = append(f.bootedOut, l); return nil },
		func(p string) error { f.removedPlist = append(f.removedPlist, p); return nil },
		func(b string) { f.killed = append(f.killed, b) },
		func(string) {}, // killGenPlatform: no-op (platform-kill covered by FEATURE 25 tests)
		func(d string) error { f.removedAll = append(f.removedAll, d); return nil },
	)

	if n != 1 {
		t.Fatalf("retired count = %d, want 1", n)
	}
	// Booted out + plist removed, but the short path was never pkill'd.
	if !contains(f.bootedOut, "shortd") {
		t.Fatalf("short-path dead gen must still be booted out, got %v", f.bootedOut)
	}
	if len(f.killed) != 0 {
		t.Fatalf("short path %q must NOT be pkill'd, got killed=%v", "/", f.killed)
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
// the FEATURE 19 EnvironmentVariables mesh marker are all recovered (the prod
// worker plist carries the marker in env, not argv).
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

	label, bin, argv, env := parsePlist(plistPath)
	if label != s.Label(RoleA) {
		t.Errorf("label = %q, want %q", label, s.Label(RoleA))
	}
	if bin != binPath {
		t.Errorf("bin = %q, want %q", bin, binPath)
	}
	// FEATURE 19: a prod worker carries the mesh marker in EnvironmentVariables,
	// not argv. The binary-format parse must recover it so the union predicate
	// still corroborates the generation.
	if env[MeshEnvKey] != "run:a" {
		t.Errorf("env[%s] = %q, want run:a (got env %v)", MeshEnvKey, env[MeshEnvKey], env)
	}
	if !isFocusdMeshWorkerPlist(env, argv) {
		t.Errorf("binary-format worker plist must corroborate the mesh, env=%v argv=%v", env, argv)
	}
}
