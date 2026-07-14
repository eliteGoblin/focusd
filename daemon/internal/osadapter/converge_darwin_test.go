//go:build darwin

package osadapter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// TestConvergeModes_ScopesTestToSandbox pins the safety + privilege properties:
//   - TEST convergence spans ONLY the throwaway sandbox domain and NEVER reaches
//     mode.System (which resolves to the real /Library unconditionally).
//   - a ROOT (sudo) install converges BOTH domains (retires user gens too).
//   - a NON-root user install converges ONLY the user domain — it never pokes
//     the system domain it has no privilege to touch (defers system cleanup).
func TestConvergeModes_ScopesTestToSandbox(t *testing.T) {
	const root, nonRoot = 0, 501
	// Test is sandbox-only regardless of euid.
	for _, euid := range []int{root, nonRoot} {
		if got := convergeModes(mode.Test, euid); len(got) != 1 || got[0] != mode.Test {
			t.Fatalf("convergeModes(Test,%d) must be {Test} only, got %v", euid, got)
		}
	}
	// Root install → both domains.
	for _, keep := range []mode.Mode{mode.User, mode.System} {
		got := convergeModes(keep, root)
		if len(got) != 2 || got[0] != mode.User || got[1] != mode.System {
			t.Fatalf("convergeModes(%s,root) must be {User,System}, got %v", keep, got)
		}
	}
	// Non-root user install → user domain only.
	if got := convergeModes(mode.User, nonRoot); len(got) != 1 || got[0] != mode.User {
		t.Fatalf("convergeModes(User,non-root) must be {User} only (defer system), got %v", got)
	}
}

// recordingRetire captures every retireGenerations side effect, including the
// FEATURE 25 platform-kill seam.
type recordingRetire struct {
	bootedOut    []string
	removedPlist []string
	killedBin    []string
	killedPlatWD []string // daemon-homes whose platform was told to die
	removedAll   []string
}

func (r *recordingRetire) run(gens []Generation, dead []DeadGeneration, keepBin, root string) int {
	return retireGenerations(gens, dead, keepBin, root,
		func(l string) error { r.bootedOut = append(r.bootedOut, l); return nil },
		func(p string) error { r.removedPlist = append(r.removedPlist, p); return nil },
		func(b string) { r.killedBin = append(r.killedBin, b) },
		func(wd string) { r.killedPlatWD = append(r.killedPlatWD, wd) },
		func(d string) error { r.removedAll = append(r.removedAll, d); return nil },
	)
}

// TestConvergeTwoGenerationsToOne (Test #1): with TWO live generations present,
// convergence keeps ONE and fully retires the OTHER — booting out its labels,
// removing its plists, killing its daemon binary AND its platform, and removing
// its workdir. Exactly one generation survives.
func TestConvergeTwoGenerationsToOne(t *testing.T) {
	root := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	keepHome := mk(".keep")
	keepBin := filepath.Join(keepHome, "keep.daemon")
	otherHome := mk(".other")
	otherBin := filepath.Join(otherHome, "other.daemon")

	gens := []Generation{
		{BinaryPath: keepBin, Workdir: keepHome,
			Labels: []string{"k1", "k2", "k3"}, PlistPaths: []string{"/p/k1", "/p/k2", "/p/k3"}},
		{BinaryPath: otherBin, Workdir: otherHome,
			Labels: []string{"o1", "o2", "o3"}, PlistPaths: []string{"/p/o1", "/p/o2", "/p/o3"}},
	}

	r := &recordingRetire{}
	n := r.run(gens, nil, keepBin, root)

	if n != 1 {
		t.Fatalf("converge must retire exactly ONE other generation, got %d", n)
	}
	// The keep is never touched.
	for _, l := range r.bootedOut {
		if l == "k1" || l == "k2" || l == "k3" {
			t.Fatalf("keep label %q must never be booted out", l)
		}
	}
	if contains(r.removedAll, keepHome) || contains(r.killedPlatWD, keepHome) {
		t.Fatalf("keep generation's workdir/platform must never be torn down")
	}
	// The other is fully retired incl. its platform.
	for _, l := range []string{"o1", "o2", "o3"} {
		if !contains(r.bootedOut, l) {
			t.Fatalf("other label %q must be booted out, got %v", l, r.bootedOut)
		}
	}
	if !contains(r.killedBin, otherBin) {
		t.Fatalf("other daemon binary must be killed, got %v", r.killedBin)
	}
	if !contains(r.killedPlatWD, otherHome) {
		t.Fatalf("other generation's platform must be killed (keyed on its daemon-home), got %v", r.killedPlatWD)
	}
	if !contains(r.removedAll, otherHome) {
		t.Fatalf("other workdir must be RemoveAll'd, got %v", r.removedAll)
	}
}

// TestRetireGenerationsKillsPlatformPath (Test #7): retireGenerations invokes
// the platform-kill seam for EVERY retired generation (live-other AND dead),
// keyed on that generation's daemon-home, and NEVER for the keep.
func TestRetireGenerationsKillsPlatformPath(t *testing.T) {
	root := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	keepHome := mk(".keep")
	keepBin := filepath.Join(keepHome, "keep.daemon")
	otherHome := mk(".other")
	deadHome := mk(".dead")

	gens := []Generation{
		{BinaryPath: keepBin, Workdir: keepHome,
			Labels: []string{"k1"}, PlistPaths: []string{"/p/k1"}},
		{BinaryPath: filepath.Join(otherHome, "other.daemon"), Workdir: otherHome,
			Labels: []string{"o1"}, PlistPaths: []string{"/p/o1"}},
	}
	dead := []DeadGeneration{
		{BinaryPath: filepath.Join(deadHome, "dead.daemon"), Workdir: deadHome,
			Labels: []string{"d1"}, PlistPaths: []string{"/p/d1"}},
	}

	r := &recordingRetire{}
	n := r.run(gens, dead, keepBin, root)
	if n != 2 {
		t.Fatalf("retired = %d, want 2 (other + dead)", n)
	}
	if !contains(r.killedPlatWD, otherHome) || !contains(r.killedPlatWD, deadHome) {
		t.Fatalf("platform-kill must fire for BOTH retired generations, got %v", r.killedPlatWD)
	}
	if contains(r.killedPlatWD, keepHome) {
		t.Fatalf("keep generation's platform must NEVER be killed, got %v", r.killedPlatWD)
	}
}

// TestPathStrictlyUnder guards the killGenerationPlatform pkill anchor.
func TestPathStrictlyUnder(t *testing.T) {
	cases := []struct {
		path, root string
		want       bool
	}{
		{"/r/.pw", "/r", true},
		{"/r/a/b/.pw", "/r", true},
		{"/r", "/r", false},           // root itself
		{"/other/.pw", "/r", false},   // outside
		{"relative/.pw", "/r", false}, // not absolute
		{"/r/.pw", "relative", false}, // relative root
		{"", "/r", false},
	}
	for _, c := range cases {
		if got := pathStrictlyUnder(c.path, c.root); got != c.want {
			t.Fatalf("pathStrictlyUnder(%q,%q) = %v, want %v", c.path, c.root, got, c.want)
		}
	}
}
