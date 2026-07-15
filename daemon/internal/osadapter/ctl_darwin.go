//go:build darwin

package osadapter

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/platdir"
	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

// launchctlCtl + laFS are mode-aware: user → gui/<uid> + ~/Library/
// LaunchAgents; system (sudo) → system domain + /Library/LaunchDaemons;
// test behaves like user. The mode is decided once at bootstrap and
// threaded in from the Spec.
type launchctlCtl struct{ m mode.Mode }

func (c launchctlCtl) domain() string { return mode.LaunchDomain(c.m, os.Getuid()) }

func (c launchctlCtl) loaded(label string) bool {
	return exec.Command("launchctl", "print", c.domain()+"/"+label).Run() == nil
}
func (c launchctlCtl) bootout(label string) error {
	return exec.Command("launchctl", "bootout", c.domain()+"/"+label).Run()
}
func (c launchctlCtl) bootstrap(pp string) error {
	// FEATURE 10 / ADR-0014: clear any prior `launchctl disable` before
	// loading. Disabling a label (the CLI form of the System Settings
	// "Allow in Background" toggle) otherwise makes launchd REFUSE to
	// bootstrap it — so a weak-moment "disable then remove" would leave the
	// rebuilt entry unloaded forever. enable takes the service target
	// (domain/label); derive the label from the plist filename. Best-effort
	// (ignore error: a not-yet-known label simply has nothing to enable).
	label := strings.TrimSuffix(filepath.Base(pp), ".plist")
	_ = exec.Command("launchctl", "enable", c.domain()+"/"+label).Run()
	out, err := exec.Command("launchctl", "bootstrap", c.domain(), pp).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

type laFS struct{ m mode.Mode }

func (f laFS) plistPath(label string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(mode.LaunchDir(f.m, home), label+".plist")
}
func (f laFS) write(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
func (laFS) remove(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Install writes the masked roster + loads all three mesh entries.
func Install(s Spec) error {
	_ = os.MkdirAll(s.Workdir, 0o755)
	return installAll(s, launchctlCtl{m: s.Mode}, laFS{m: s.Mode}, newWorkdirRoster(s.Workdir))
}

// Uninstall boots out + removes all three entries + the roster file.
func Uninstall(testMode bool) error {
	m := modeFromTestFlag(testMode)
	// Uninstall via the legacy fixed-label path doesn't know the workdir;
	// the disguised-install teardown is UninstallProd. Test-mode uninstall
	// uses the given workdir via the caller; here we have none, so skip the
	// roster removal (the prod teardown path handles the real file).
	return uninstallAll(Spec{Mode: m}, launchctlCtl{m: m}, laFS{m: m}, nil)
}

// EnsureAll recreates any missing mesh entry (mutual self-healing) and
// self-heals the masked roster file from the in-memory roster.
func EnsureAll(s Spec) ([]Role, error) {
	var rs rosterIO
	if s.Workdir != "" {
		rs = newWorkdirRoster(s.Workdir)
	}
	return ensureAll(s, launchctlCtl{m: s.Mode}, laFS{m: s.Mode}, rs)
}

// IsLoaded reports whether a role's launchd entry is registered.
func IsLoaded(testMode bool, r Role) bool {
	return launchctlCtl{m: modeFromTestFlag(testMode)}.loaded(LabelFor(testMode, r))
}

// modeFromTestFlag maps the legacy test-mode bool to a Mode: test when
// set, otherwise the real deployment mode resolved from euid (sudo →
// system, else user).
func modeFromTestFlag(testMode bool) mode.Mode {
	if testMode {
		return mode.Test
	}
	return mode.Resolve()
}

// CurInstall describes a discovered, on-disk focusd mesh install —
// what `daemon self-update` swaps and `daemon uninstall` removes. It
// is the owner-driven view of a disguised install whose random labels
// and paths are NOT known a priori; everything is recovered by
// structural scan + Ed25519 signature recognition.
//
// Named CurInstall (not Install) because `func Install(Spec) error`
// already owns that identifier in this package.
//
// Each field is populated when the scan finds the full mesh (3 plists
// whose ProgramArguments point at the same Ed25519-verified binary AND
// carry the same --roster label set). When the mesh is incomplete the
// entries that were found are still returned in the slices in scan order
// (caller decides whether that is fatal).
//
// FEATURE 10 / ADR-0014: the three mesh labels are now INDEPENDENT (no
// shared base), so correlation is by verified-binary + the --roster argv
// the installer baked into every plist — NOT by a shared label stem.
type CurInstall struct {
	Mode       mode.Mode     // user | system (Test is not discovered this way)
	Roster     []string      // the 3 independent mesh labels (AllRoles order), from the masked workdir file (FEATURE 14); --roster argv is the old-plist fallback
	Workdir    string        // recovered as filepath.Dir(BinaryPath) (FEATURE 14); --workdir argv is the old-plist fallback
	BinaryPath string        // the ProgramArguments[0] binary path — the FEATURE 14 correlation key
	Interval   time.Duration // reconcile interval recovered from --interval argv (0 if absent / new plist)
	PlistPaths []string      // up to 3, in scan order
	Labels     []string      // up to 3, in scan order (aligned with PlistPaths)
}

// FindCurrentInstall scans the LaunchDir for the given mode and returns
// the focusd install rooted there (if any). A plist is treated as ours
// only when ProgramArguments[0] passes Ed25519 verification with the
// embedded public key — the design's signature recognition (see
// daemon_design.md §6). Returns (zero, nil) when no genuine install is
// found, an error only for filesystem failure.
//
// All three mesh entries must agree on the same Ed25519-verified binary
// path (FEATURE 14 / ADR-0018 correlation key); otherwise the function
// returns whatever it could parse and the caller decides. The roster and
// workdir are then recovered off-argv (masked file + Dir(bin)).
// Verifier is the signature-check seam — production passes sig.VerifyFile
// (the real Ed25519 check against the embedded public key); tests pass
// a fake to avoid needing the offline private key in CI. Replaces the
// prior package-global `verifyFn`, which was a data-race hazard if any
// test ever ran subtests in parallel (Go-reviewer HIGH).
type Verifier func(path string) (bool, error)

// FindCurrentInstall scans laDir for plists, verifies each candidate
// binary with `verify`, and returns the install if any. Pass
// sig.VerifyFile from production; tests pass a fake.
func FindCurrentInstall(m mode.Mode, verify Verifier) (CurInstall, error) {
	if verify == nil {
		verify = sig.VerifyFile
	}
	home, _ := os.UserHomeDir()
	laDir := mode.LaunchDir(m, home)
	entries, rerr := os.ReadDir(laDir)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return CurInstall{}, nil
		}
		return CurInstall{}, rerr
	}
	cur := CurInstall{Mode: m}
	// lastArgv keeps the argv of the most recently matched plist; matchedArgvs
	// keeps EVERY matched plist's argv. The post-loop workdir/roster recovery
	// falls back to OLD-plist argv flags (--workdir/--roster) when the masked
	// file / Dir(bin) path isn't available. In a HALF-MIGRATED install only
	// SOME plists still carry --roster in argv (new minimized plists dropped
	// it, FEATURE 14 / ADR-0018), so the roster fallback must scan ALL matched
	// argvs — not just the last one, which may be a minimized plist with no
	// --roster.
	var lastArgv []string
	var matchedArgvs [][]string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".plist") {
			continue
		}
		pp := filepath.Join(laDir, e.Name())
		// FindCurrentInstall correlates on the Ed25519-verified binary path
		// (argv[0]); the env marker is not needed here (discarded).
		label, bin, argv, _ := parsePlist(pp)
		if label == "" || bin == "" {
			continue
		}
		if ok, verr := verify(bin); verr != nil || !ok {
			continue // not a genuine focusd binary → not ours
		}
		// FEATURE 14 / ADR-0018: correlation key is the shared, Ed25519-
		// verified BINARY PATH across the three plists — NOT the --roster
		// argv (new minimized plists no longer carry it). argv[0] is shared
		// across all three mesh members and is a strictly stronger key: it
		// is the verified install identity. Establish it from the FIRST
		// matched plist; subsequent matches must agree.
		if cur.BinaryPath == "" {
			cur.BinaryPath = bin
		} else if cur.BinaryPath != bin {
			continue // unrelated focusd install (different rotation)
		}
		// Recover the interval from argv if an OLD plist still bakes it;
		// new plists omit it (the worker cadence is a fixed constant).
		if cur.Interval == 0 {
			cur.Interval = intervalFromArgv(argv)
		}
		cur.PlistPaths = append(cur.PlistPaths, pp)
		cur.Labels = append(cur.Labels, label)
		lastArgv = argv
		matchedArgvs = append(matchedArgvs, argv)
	}
	// Recover the workdir + roster ONCE, after the binary path is known.
	// FEATURE 14 / ADR-0018:
	//   - Workdir: the disguised binary is relocated INSIDE the workdir, so
	//     filepath.Dir(BinaryPath) IS the workdir. workdirFromArgv is the
	//     fallback only for an OLD plist whose binary path has no parent.
	//   - Roster: read from the masked workdir file (the single source of
	//     truth). For an OLD plist where the file isn't present, fall back to
	//     the --roster argv the installer baked.
	if cur.BinaryPath != "" {
		cur.Workdir = recoverWorkdir(cur.BinaryPath, lastArgv)
		cur.Roster = recoverRoster(cur.Workdir, matchedArgvs)
	}
	return cur, nil
}

// recoverWorkdir resolves a discovered install's workdir (FEATURE 14 /
// ADR-0018). The disguised binary lives inside the workdir, so the binary's
// parent directory IS the workdir; workdirFromArgv (an OLD plist's --workdir)
// is only the fallback when Dir(bin) yields nothing usable.
func recoverWorkdir(bin string, argv []string) string {
	if wd := WorkdirFromBinary(bin); wd != "" {
		return wd
	}
	return workdirFromArgv(argv)
}

// recoverRoster resolves a discovered install's roster (FEATURE 14 /
// ADR-0018). The masked workdir file is the single source of truth; an OLD
// plist's --roster argv is the fallback for installs predating the masked
// file (or when the file is unreadable).
//
// argvs is EVERY matched plist's argv: in a half-migrated install only the
// OLD plist(s) still carry --roster (new minimized plists dropped it), and
// scan order may make a minimized plist the last one, so we scan all of them
// and take the first that yields a non-nil roster. Returns nil (the accepted
// degraded mode) only when no plist carries a roster AND the masked file is
// absent/unreadable.
func recoverRoster(workdir string, argvs [][]string) []string {
	if workdir != "" {
		// Require the EXACT mesh size. core.ReadRoster validates non-empty
		// labels but not count; a truncated/edited .roster yielding a short
		// slice would let Spec.Label backfill missing positions with dev
		// labels and corrupt correlation/rebuild — treat that as unreadable
		// and fall through to the argv fallback (Copilot review).
		if labels, err := core.ReadRoster((&core.Store{Dir: workdir}).RosterPath()); err == nil && len(labels) == len(AllRoles) {
			return labels
		}
	}
	for _, argv := range argvs {
		if labels := rosterFromArgv(argv); labels != nil {
			return labels
		}
	}
	return nil
}

// MeshStatus reports how many of the discovered mesh roles are currently
// loaded in launchd. It discovers the install by Ed25519 signature and
// queries each label internally, returning ONLY counts — the disguised
// labels never cross this boundary, so a caller like `daemon status`
// physically cannot leak them. `found` is false when no genuine install
// was discovered (total 0); a filesystem failure is returned as err.
func MeshStatus(m mode.Mode) (loaded, total int, found bool, err error) {
	cur, ferr := FindCurrentInstall(m, sig.VerifyFile)
	if ferr != nil {
		return 0, 0, false, ferr
	}
	if len(cur.Labels) == 0 {
		return 0, 0, false, nil
	}
	c := launchctlCtl{m: m}
	for _, lbl := range cur.Labels {
		if c.loaded(lbl) {
			loaded++
		}
	}
	return loaded, len(cur.Labels), true, nil
}

// UninstallProd removes a disguised user/system install whose labels are
// randomized/unknown. It uses FindCurrentInstall for the scan, then
// bootouts + removes plists + pkills the binary. Owner-driven teardown
// of a hidden install without needing the random names.
func UninstallProd() (removed []string, err error) {
	m := mode.Resolve()
	cur, ferr := FindCurrentInstall(m, sig.VerifyFile)
	if ferr != nil {
		return nil, ferr
	}
	if cur.BinaryPath == "" {
		return nil, nil
	}
	// Best-effort process kill is fine for uninstall (we are tearing
	// the whole install down, no surviving daemon will see argv overlap).
	_ = exec.Command("pkill", "-f", cur.BinaryPath).Run()
	c := launchctlCtl{m: m}
	for i, label := range cur.Labels {
		_ = c.bootout(label)
		_ = os.Remove(cur.PlistPaths[i])
		removed = append(removed, label)
	}
	// Remove the masked roster file LAST (best-effort): the mesh it
	// described is gone, so a mid-uninstall survivor could recover until
	// here. A leftover roster is harmless dead weight, not a correctness
	// problem — so a remove failure does not fail the teardown.
	if cur.Workdir != "" {
		_ = newWorkdirRoster(cur.Workdir).removeRoster()
	}
	return removed, nil
}

// Generation is one distinct, on-disk focusd mesh install identified by its
// Ed25519-verified binary path (FEATURE 17, Item 3). A path-rotating
// self-update or a wiped-state reinstall can leave multiple generations'
// plists in the LaunchDir at once; cleanup retires all but the surviving one.
type Generation struct {
	BinaryPath string   // the shared, Ed25519-verified ProgramArguments[0]
	Workdir    string   // Dir(BinaryPath) — the disguised binary lives in it
	Labels     []string // every label whose plist points at BinaryPath
	PlistPaths []string // aligned with Labels (same scan order)
}

// DeadGeneration is a focusd mesh generation whose binary has been DELETED —
// the zombie left behind by a workdir-delete/recovery cycle (FEATURE 17
// follow-up, TC-21). Its ProgramArguments[0] names a path that no longer
// exists, so it can NOT be Ed25519-verified; the only signal that it is ours is
// the mesh worker marker on at least one of its plists. Its launchd entries and
// orphan platform/daemon processes still reference the (now-dangling) binary
// path in their argv, so retirement boots out the labels, removes the plists,
// and pkill-matches the dangling path to reap the orphans.
type DeadGeneration struct {
	BinaryPath string   // the DELETED ProgramArguments[0] path (still in the plist + the orphans' argv)
	Workdir    string   // Dir(BinaryPath) — may itself be gone (then RemoveAll is a guarded no-op)
	Labels     []string // every label whose plist points at the dead BinaryPath
	PlistPaths []string // aligned with Labels (same scan order)
}

// genAccum accumulates one generation's plists during the scan.
type genAccum struct {
	binaryPath string
	workdir    string
	labels     []string
	plistPaths []string
	meshSeen   bool // at least one plist carried the --mesh worker marker
}

// DiscoverAllGenerations scans the LaunchDir for the mode and groups every
// genuine focusd plist by its DISTINCT Ed25519-verified binary path. The
// signature check on ProgramArguments[0] is the authoritative safety belt — a
// real third-party/vendor binary never verifies against our embedded key, so
// no unrelated launchd job can ever be grouped (let alone retired). The mesh
// WORKER marker is a corroborating signal: a binary is treated as a real mesh
// generation only when at least one of its plists is a worker — carrying the
// FEATURE 19 env marker (MeshEnvKey="run:<role>") OR the legacy --mesh argv
// (isFocusdMeshWorkerPlist). The ensure role's plist has neither, but it is
// grouped in via the shared verified binary. The out-of-band watchdog has NO
// LaunchDir plist (it is cron-driven)
// so it is never seen here — by construction it can never be discovered or
// retired. verify is the signature seam (nil ⇒ sig.VerifyFile); tests inject
// a fake. Generations are returned in first-seen order.
//
// FEATURE 17 follow-up (TC-21): a generation whose binary was DELETED by a
// workdir-delete/recovery cycle can no longer be Ed25519-verified — os.ReadFile
// returns ENOENT, so verify errors. Such a plist is NOT silently dropped (the
// old behavior that let invisible-zombie generations + orphan platforms
// accumulate). Instead it is grouped into the SECOND return value, the dead
// generations, keyed by its dangling binary path and corroborated by the SAME
// mesh worker marker — so a non-focusd vendor plist whose binary merely happens
// to be absent is never treated as ours. A binary that EXISTS but fails the
// signature (a genuine vendor binary) is still skipped, as before.
func DiscoverAllGenerations(m mode.Mode, verify Verifier) (live []Generation, dead []DeadGeneration, err error) {
	if verify == nil {
		verify = sig.VerifyFile
	}
	home, _ := os.UserHomeDir()
	laDir := mode.LaunchDir(m, home)
	entries, rerr := os.ReadDir(laDir)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return nil, nil, nil
		}
		return nil, nil, rerr
	}
	var order []string // live binary paths in first-seen order
	byBin := map[string]*genAccum{}
	var deadOrder []string // dead (deleted) binary paths in first-seen order
	deadByBin := map[string]*genAccum{}
	// accumulate groups one plist into the given bin-keyed map, mirroring the
	// live and dead grouping so the corroboration logic is identical.
	accumulate := func(into map[string]*genAccum, ord *[]string, bin, label, pp string, argv []string, env map[string]string) {
		g := into[bin]
		if g == nil {
			g = &genAccum{binaryPath: bin, workdir: WorkdirFromBinary(bin)}
			into[bin] = g
			*ord = append(*ord, bin)
		}
		g.labels = append(g.labels, label)
		g.plistPaths = append(g.plistPaths, pp)
		// FEATURE 19 union: a NEW plist corroborates via its env worker marker
		// (MeshEnvKey="run:<role>"), an OLD plist via the legacy --mesh argv.
		// The ensure role corroborates neither — an ensure-only generation is
		// not a real mesh (preserved from FEATURE 17).
		if isFocusdMeshWorkerPlist(env, argv) {
			g.meshSeen = true
		}
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".plist") {
			continue
		}
		pp := filepath.Join(laDir, e.Name())
		label, bin, argv, env := parsePlist(pp)
		if label == "" || bin == "" {
			continue
		}
		ok, verr := verify(bin)
		switch {
		case verr == nil && ok:
			// Live generation: a present, Ed25519-verified binary.
			accumulate(byBin, &order, bin, label, pp, argv, env)
		case verr != nil && errors.Is(verr, fs.ErrNotExist):
			// Dead generation: ProgramArguments[0] names a binary that no
			// longer exists (its workdir was deleted by a recovery cycle). A
			// file that is gone cannot be Ed25519-verified, so the mesh worker
			// marker is the ONLY signal this orphan plist is ours — group +
			// corroborate it exactly like a live generation, keyed by the
			// (now-dangling) bin path so the sibling ensure plist (no marker)
			// is swept in via the shared path.
			accumulate(deadByBin, &deadOrder, bin, label, pp, argv, env)
		default:
			// ok==false: a binary that EXISTS but fails the signature (a real
			// vendor binary) → never ours. A non-ENOENT verify error (a present
			// but malformed/unreadable file) → cannot classify → skip. Either
			// way, leave it untouched.
			continue
		}
	}
	for _, bin := range order {
		g := byBin[bin]
		if !g.meshSeen {
			continue // verified binary but no --mesh plist → not a real mesh
		}
		live = append(live, Generation{
			BinaryPath: g.binaryPath,
			Workdir:    g.workdir,
			Labels:     g.labels,
			PlistPaths: g.plistPaths,
		})
	}
	for _, bin := range deadOrder {
		g := deadByBin[bin]
		if !g.meshSeen {
			continue // deleted binary but no mesh marker → not ours (vendor)
		}
		dead = append(dead, DeadGeneration{
			BinaryPath: g.binaryPath,
			Workdir:    g.workdir,
			Labels:     g.labels,
			PlistPaths: g.plistPaths,
		})
	}
	return live, dead, nil
}

// RetireOtherGenerations discovers every focusd generation and tears down each
// one whose binary path differs from keepBinaryPath (FEATURE 17, Item 3). For
// each retired generation it boots out every label, removes every plist, best-
// effort kills the old binary's processes, and (GUARDED by safeToRemoveWorkdir)
// removes the old workdir. Best-effort throughout: it returns ONLY the count of
// retired generations (never the disguised labels/paths), and a single retire
// step's failure does not abort the rest. Called AFTER a successful install so
// the new generation is already up; NEVER from the self-update path (in-place
// rotation transiently looks like two generations).
//
// FEATURE 17 follow-up (TC-21): it ALSO retires DEAD generations — zombies whose
// binary was deleted by a workdir-delete/recovery cycle. Their launchd entries
// and orphan platform/daemon processes persist invisibly otherwise (the old
// "retired 1 while ≥2 live generations remain" bug). pkill -f against the
// dangling binary path matches the orphans' argv and reaps them.
//
// supportRoot is the containment root for the os.RemoveAll blast radius: a
// workdir is only deleted when it is strictly nested under it (safeToRemoveWorkdir).
// It is an EXPLICIT param — NOT recomputed from mode.SupportRoot(m, home) — so a
// test-mode install (whose SupportRoot(Test, …) would otherwise resolve to the
// REAL ~/Library/Application Support and let a sandbox install delete real
// generation workdirs) passes its sandbox root and can never escape it. The
// caller (installMesh) passes the same sandboxed local it hands the platform sweep.
func RetireOtherGenerations(m mode.Mode, keepBinaryPath, supportRoot string) (int, error) {
	// Refuse to retire ANYTHING without a surviving generation to keep: an
	// empty keepBinaryPath would make every discovered generation "other" and
	// tear the whole mesh down (the bootout + os.RemoveAll blast radius). A
	// caller with no keep target is a bug, not a request to wipe everything.
	if keepBinaryPath == "" {
		return 0, fmt.Errorf("retire: keepBinaryPath must not be empty")
	}
	gens, dead, err := DiscoverAllGenerations(m, sig.VerifyFile)
	if err != nil {
		return 0, err
	}
	c := launchctlCtl{m: m}
	// f0593fb (HF1 fix) supplies the containment root as an EXPLICIT param;
	// FEATURE 25 adds killGenerationPlatform so a retired generation's PLATFORM
	// child is torn down too (not just its daemon binary). Combine both.
	return retireGenerations(gens, dead, keepBinaryPath, supportRoot,
		c.bootout, os.Remove, pkillBinary, killGenerationPlatform(supportRoot), os.RemoveAll), nil
}

// CountOtherGenerations is the READ-ONLY counterpart of RetireOtherGenerations
// (FEATURE 17 generation cleanliness / `daemon status`): it reuses the SAME
// DiscoverAllGenerations scan but retires NOTHING, returning only how many
// OTHER focusd generations exist besides the one rooted at keepBinaryPath. It is
// the sanctioned way for status to answer the owner's "new version starts clean,
// no dup / no old version" acceptance signal without enumerating processes.
//
// An "other" generation is either a LIVE generation whose Ed25519-verified
// binary path differs from keepBinaryPath, or ANY dead/zombie generation (a
// workdir-delete/recovery leftover whose plists + orphan processes persist). A
// clean install returns 0 — the only generation present IS the keep.
//
// keepBinaryPath is the current good install's binary path (FindCurrentInstall's
// FEATURE 14 correlation key); it MUST be non-empty — with no keep to compare
// against, every discovered generation would count as "other", so an empty
// keepBinaryPath is a caller bug and returns an error (status buckets it to
// "unknown" rather than falsely counting the live generation as an orphan).
//
// Returns a COUNT ONLY — never the disguised labels/paths — so a caller like
// `daemon status` physically cannot leak a teardown string. NOTE: the returned
// err can carry a *PathError from the underlying scan (its .Error() embeds a
// filesystem path), so callers must never log/render it verbatim — the sole
// caller (gather_darwin) discards it into a boolean "unknown" flag.
//
// A filesystem failure at the scan is surfaced as err so status buckets it to
// "unknown" rather than ever fabricating a "clean" 0.
func CountOtherGenerations(m mode.Mode, keepBinaryPath string) (others int, err error) {
	if keepBinaryPath == "" {
		return 0, fmt.Errorf("count generations: keepBinaryPath must not be empty")
	}
	live, dead, derr := DiscoverAllGenerations(m, sig.VerifyFile)
	if derr != nil {
		return 0, derr
	}
	return countOtherGenerations(live, dead, keepBinaryPath), nil
}

// countOtherGenerations is the pure core of CountOtherGenerations, split out so
// the count logic is unit-tested with plain slices (no real launchd / FS scan),
// mirroring how retireGenerations is the seam-injected core of
// RetireOtherGenerations. An "other" generation is a live generation whose
// binary path differs from keepBinaryPath, PLUS every dead generation (its
// deleted binary can never equal the surviving keep). Paths are Clean'd before
// comparison so a trailing-slash / non-canonical keep never mis-counts the keep
// itself as an orphan. A dead generation carries the SAME defensive keep-equality
// guard as retireGenerations' dead loop — by construction a dead (deleted) binary
// can never equal the present, verified keep, but the twin read/write paths stay
// consistent so a future divergence can't silently over-count.
func countOtherGenerations(live []Generation, dead []DeadGeneration, keepBinaryPath string) int {
	keep := filepath.Clean(keepBinaryPath)
	others := 0
	for _, g := range live {
		if filepath.Clean(g.BinaryPath) != keep {
			others++
		}
	}
	for _, d := range dead {
		if filepath.Clean(d.BinaryPath) != keep {
			others++
		}
	}
	return others
}

// SweepOrphanWorkdirs deletes daemon-home workdirs that survive on disk with NO
// loaded plist or running process backing them — the residual a teardown /
// recovery / re-install cycle leaves behind (FEATURE 17 follow-up, TC-21).
// RetireOtherGenerations only catches generations still visible to a plist scan
// (live "other" + dead-binary "zombie" generations); a generation whose plist
// was already removed leaves ONLY orphaned files on disk, invisible to that scan.
// This sweep is the disk-side complement: after any install exactly ONE
// daemon-home (the keep) survives.
//
// FEATURE 26 (destructive-safety core): it scans the IMMEDIATE children of the
// support root and removes each directory carrying the DAEMON-HOME CONTENT
// sentinel (platdir.IsDaemonHome — a sentinel file whose bytes un-mask to the
// daemon-home magic) that is NOT the keepWorkdir. This REPLACES the previous
// "hidden-dot + contains state.db" heuristic, which — once names lost their
// leading dot — would have scanned real app folders and deleted any that happened
// to hold a file named state.db. Now nothing but a positive daemon-home magic
// match can gate a RemoveAll: a platform-workdir (different magic) and a real app
// folder (no magic) are never candidates. Every removal is additionally GATED by
// safeToRemoveWorkdir (absolute, strictly under the support root, not the keep,
// not an ancestor of the keep) — the same belt RetireOtherGenerations uses.
//
// keepWorkdir is the new install's daemon-home (Dir of the relocated binary).
//
// supportRoot is the scan root AND the containment root for os.RemoveAll — an
// EXPLICIT param (mirroring SweepStalePlatformWorkdirs), NOT recomputed from
// mode.SupportRoot(m, home). That recompute was the storage-separation defect:
// SupportRoot(Test, …) resolves to the REAL ~/Library/Application Support, so a
// test-mode install would scan and delete REAL workdirs. The caller (installMesh)
// passes the sandboxed local it already computed, so a test-mode sweep is
// confined to the sandbox.
//
// Best-effort throughout: it returns only the count of removed workdirs (never
// the disguised paths) and never errors out the install.
func SweepOrphanWorkdirs(supportRoot, keepWorkdir string) (removed int, err error) {
	entries, rerr := os.ReadDir(supportRoot)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return 0, nil
		}
		return 0, rerr
	}
	keep := filepath.Clean(keepWorkdir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(supportRoot, e.Name())
		if filepath.Clean(dir) == keep {
			continue // the surviving daemon-home — never sweep it
		}
		// A platform-workdir carries a DIFFERENT magic; leave it to
		// SweepStalePlatformWorkdirs. (IsDaemonHome is already false for it, so
		// this is belt-and-suspenders clarity.)
		if platdir.IsPlatformWorkdir(dir) {
			continue
		}
		// CONTENT gate: only a positive daemon-home sentinel match. Real app
		// folders (no magic) are skipped here — nothing else reaches the RemoveAll.
		if !platdir.IsDaemonHome(dir) {
			continue
		}
		// GUARD: only RemoveAll a dir strictly under the support root that is
		// neither the keep nor an ancestor of it (the same belt the generation
		// retirement uses). Best-effort: a remove failure is skipped, not fatal.
		if safeToRemoveWorkdir(dir, supportRoot, keepWorkdir) {
			if rmErr := os.RemoveAll(dir); rmErr == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// minBinPathLen is the floor on a binary path before retirement will pkill -f
// it. Any real disguised install path is far longer; a short value like "/"
// (a corrupt/dead-gen ProgramArguments[0]) must never expand into a broad
// `pkill -f /` that reaps unrelated processes.
const minBinPathLen = 20 // shorter than any real disguised install path

// retireGenerations is the seam-injected core of RetireOtherGenerations, split
// out so the teardown ordering + the os.RemoveAll path-sanity gating are unit-
// tested with fakes (no real launchd / FS deletion). bootout/removePlist/
// killBin/removeAll are the side-effecting seams. Returns the number of
// generations retired (live others whose BinaryPath != keepBinaryPath, PLUS
// every dead/zombie generation).
//
// FEATURE 25: killGenPlatform is the platform-kill seam — for every retired
// generation it is called with that generation's daemon-home (workdir) so the
// generation's PLATFORM process (a separate binary under a separate
// platform-workdir, HF1) is torn down too. killBin only reaps the DAEMON binary
// path; without this, a retired generation's orphaned platform child survived
// and accreted (the root-cause hole). The keep generation is skipped entirely,
// so its platform is never touched here.
func retireGenerations(
	gens []Generation, dead []DeadGeneration, keepBinaryPath, supportRoot string,
	bootout func(string) error,
	removePlist func(string) error,
	killBin func(string),
	killGenPlatform func(daemonHome string),
	removeAll func(string) error,
) int {
	// Defense in depth (mirrors RetireOtherGenerations): with no keep target
	// EVERY generation would be "other" and get torn down. Retire nothing.
	if keepBinaryPath == "" {
		return 0
	}
	keepWorkdir := filepath.Dir(keepBinaryPath)
	retired := 0
	// retire performs the common teardown — bootout every label, remove every
	// plist, best-effort kill the binary's processes, and (GUARDED) RemoveAll
	// the workdir. Shared by live-other and dead generations so the ordering +
	// gating are identical.
	retire := func(bin, workdir string, labels, plistPaths []string) {
		for i, lbl := range labels {
			_ = bootout(lbl)
			if i < len(plistPaths) {
				_ = removePlist(plistPaths[i])
			}
		}
		// GUARD: never pkill -f a short path. A dead-generation plist whose
		// ProgramArguments[0] is "/" (or any short root-ish path) must NOT
		// expand into `pkill -f /` and reap unrelated processes. Real disguised
		// install paths are far longer than minBinPathLen.
		if len(bin) > minBinPathLen {
			killBin(bin) // best-effort; no surviving daemon shares this path
		}
		// FEATURE 25: also tear down this generation's PLATFORM process (a
		// separate binary under its own platform-workdir, HF1). killBin above only
		// matched the DAEMON binary; a retired generation's orphaned platform must
		// die too or it survives + accretes. Keyed on the daemon-home so the seam
		// resolves the platform-workdir pointer itself (best-effort, guarded).
		if killGenPlatform != nil && workdir != "" {
			killGenPlatform(workdir)
		}
		// GUARD: only RemoveAll a workdir that is strictly under the mode's
		// support root, is not the keep workdir, and is not an ancestor of it.
		if safeToRemoveWorkdir(workdir, supportRoot, keepWorkdir) {
			_ = removeAll(workdir)
		}
		retired++
	}
	for _, g := range gens {
		if g.BinaryPath == keepBinaryPath {
			continue // the surviving generation — never retire it
		}
		retire(g.BinaryPath, g.Workdir, g.Labels, g.PlistPaths)
	}
	// Dead/zombie generations: binary already deleted, so by construction never
	// the keep (whose binary is present + verified). pkill -f against the
	// dangling path reaps the orphan platform/daemon procs still showing it in
	// argv. Defensive equality guard against a pathological keep == dead bin.
	for _, d := range dead {
		if d.BinaryPath == keepBinaryPath {
			continue
		}
		retire(d.BinaryPath, d.Workdir, d.Labels, d.PlistPaths)
	}
	return retired
}

// pkillBinary best-effort kills any process whose argv matches bin. Used only
// during generation retirement, where the binary is about to be removed and no
// surviving daemon shares its (rotated) path.
func pkillBinary(bin string) { _ = exec.Command("pkill", "-f", bin).Run() }

// plutilToXML converts a (possibly binary-format) plist to XML on stdout via
// `plutil -convert xml1`. Returns "" on any failure so callers degrade to the
// raw bytes instead of crashing.
func plutilToXML(path string) string {
	out, err := exec.Command("plutil", "-convert", "xml1", "-o", "-", path).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// interTagSpaceRE matches whitespace between two adjacent XML tags.
var interTagSpaceRE = regexp.MustCompile(`>\s+<`)

// collapseInterTagSpace removes whitespace between adjacent tags so plutil's
// multi-line xml1 output (e.g. "</key>\n<string>") presents the
// "</key><string>" / "<array><string>" adjacency the substring scanner in
// parsePlist expects. Values inside a single <string> element have no inner
// tags, so they are untouched.
func collapseInterTagSpace(s string) string { return interTagSpaceRE.ReplaceAllString(s, "><") }

// parsePlist extracts the Label, the real BINARY path, the full argv, and the
// plist's EnvironmentVariables map from one of our generated plists. len(argv)==0
// and env==nil on parse failure.
//
// FEATURE 26 (layer a): a non-test mesh plist now carries a Program key (the real
// binary launchd execs) and a spoofed ProgramArguments[0] (the display argv[0]).
// bin is taken from Program when present, so discovery/verification still keys on
// the REAL binary path even though argv[0] is now a generic token. A legacy/test
// plist has no Program key ⇒ bin falls back to ProgramArguments[0] (== argv[0]),
// exactly as before. argv is always the ProgramArguments array (the token +
// arguments), so the roster/workdir/interval argv fallbacks are unaffected.
//
// FEATURE 19: the env map carries the mesh role marker (MeshEnvKey) that the
// minimized prod argv no longer holds — DiscoverAllGenerations unions it with
// the legacy --mesh argv marker so generation cleanup recognises both new and
// old plists (see isFocusdMeshWorkerPlist).
func parsePlist(path string) (label, bin string, argv []string, env map[string]string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", nil, nil
	}
	s := string(b)
	// Harden against BINARY-format plists (FEATURE 17): the substring scanner
	// below only understands XML. A binary plist (magic "bplist00") or any
	// non-XML content is converted via `plutil -convert xml1` first; the
	// converted output is whitespace-normalized so the "</key><string>" /
	// "<array><string>" adjacency the scanner expects holds. Degrade, don't
	// crash: on any plutil failure we keep the raw bytes and the scan simply
	// finds nothing (the plist is skipped).
	if !strings.Contains(s, "<plist") {
		if conv := plutilToXML(path); conv != "" {
			s = collapseInterTagSpace(conv)
		}
	}
	env = parseEnvDict(s)
	label = between(s, "<key>Label</key><string>", "</string>")
	// FEATURE 26: the real binary is under the Program key (the search string ends
	// in "</key><string>", so it cannot false-match "<key>ProgramArguments</key>",
	// which is followed by "<array>"). Absent ⇒ legacy/test plist → bin falls back
	// to ProgramArguments[0] below.
	program := between(s, "<key>Program</key><string>", "</string>")
	i := strings.Index(s, "<key>ProgramArguments</key><array>")
	if i < 0 {
		return label, program, nil, env
	}
	// Walk the inner <string>...</string> entries up to the closing
	// </array>; preserves order and handles the "--flag value" pair
	// form the daemon emits.
	tail := s[i+len("<key>ProgramArguments</key><array>"):]
	end := strings.Index(tail, "</array>")
	if end < 0 {
		return label, "", nil, env
	}
	inner := tail[:end]
	for {
		j := strings.Index(inner, "<string>")
		if j < 0 {
			break
		}
		k := strings.Index(inner[j:], "</string>")
		if k < 0 {
			break
		}
		v := strings.TrimSpace(inner[j+len("<string>") : j+k])
		argv = append(argv, v)
		inner = inner[j+k+len("</string>"):]
	}
	// FEATURE 26: prefer the Program key (the REAL binary) as bin; only fall back
	// to ProgramArguments[0] for a legacy/test plist that has no Program key (there
	// argv[0] IS the binary). This keeps discovery/verification/generation-grouping
	// keyed on the real binary path even though argv[0] is now a spoof token.
	if program != "" {
		bin = program
	} else if len(argv) > 0 {
		bin = argv[0]
	}
	return label, bin, argv, env
}

// parseEnvDict extracts the plist's EnvironmentVariables <dict> into a map of
// <key>→<string> (FEATURE 19). Returns nil when there is no EnvironmentVariables
// dict (an OLD plist, the test-mode plist, or a vendor plist). It walks the same
// "<key>…</key><string>…</string>" adjacency the ProgramArguments scanner relies
// on; binary plists are normalized via collapseInterTagSpace upstream so the
// adjacency holds.
func parseEnvDict(s string) map[string]string {
	const head = "<key>EnvironmentVariables</key><dict>"
	i := strings.Index(s, head)
	if i < 0 {
		return nil
	}
	tail := s[i+len(head):]
	end := strings.Index(tail, "</dict>")
	if end < 0 {
		return nil
	}
	inner := tail[:end]
	out := map[string]string{}
	for {
		ks := strings.Index(inner, "<key>")
		if ks < 0 {
			break
		}
		ke := strings.Index(inner[ks:], "</key>")
		if ke < 0 {
			break
		}
		key := strings.TrimSpace(inner[ks+len("<key>") : ks+ke])
		rest := inner[ks+ke+len("</key>"):]
		vs := strings.Index(rest, "<string>")
		if vs < 0 {
			break
		}
		ve := strings.Index(rest[vs:], "</string>")
		if ve < 0 {
			break
		}
		out[key] = strings.TrimSpace(rest[vs+len("<string>") : vs+ve])
		inner = rest[vs+ve+len("</string>"):]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// launchctlProber introspects launchd for the health poll. The label
// is loaded iff `launchctl print` returns 0; a worker has a PID iff
// the `state = …` or `pid = …` line in the print output reports one.
type launchctlProber struct{ m mode.Mode }

func (p launchctlProber) domain() string { return mode.LaunchDomain(p.m, os.Getuid()) }

func (p launchctlProber) isLoaded(label string) bool {
	return exec.Command("launchctl", "print", p.domain()+"/"+label).Run() == nil
}

func (p launchctlProber) hasPID(label string) bool {
	out, err := exec.Command("launchctl", "print", p.domain()+"/"+label).Output()
	if err != nil {
		return false
	}
	// `launchctl print` output is verbose; look for "pid = N" (N > 0).
	// Format: "    pid = 12345" or "state = running"+"pid = N".
	for _, line := range strings.Split(string(out), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "pid = ") {
			v := strings.TrimSpace(strings.TrimPrefix(l, "pid = "))
			if v != "" && v != "0" {
				return true
			}
		}
	}
	return false
}

// binPlacerFS writes raw bytes atomically with exec mode. On macOS the
// Go linker's adhoc Mach-O signature is part of the file content, so a
// plain rename of the verified bytes is enough — we do NOT shell out
// to `codesign` here. (The CDHash is fresh because the file is a
// fresh inode at a fresh path; that is the whole AMFI workaround.)
type binPlacerFS struct{}

func (binPlacerFS) place(srcBytes []byte, dstPath string) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err != nil {
		return err
	}
	tmp := dstPath + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, bytes.NewReader(srcBytes)); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dstPath)
}

func (binPlacerFS) remove(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SelfUpdateProd is the darwin entry-point that wires the real launchd
// controller + plist filesystem + launchctl-print prober + atomic
// binary placer into the pure SelfUpdate orchestration. cur is the
// discovered current install (FindCurrentInstall); newSpec carries the
// rotated SelfPath/Base; newBin is the verified daemon bytes.
func SelfUpdateProd(
	cur CurInstall, newSpec Spec, newBin []byte,
	healthyTimeout, probeInterval time.Duration, keepOld bool,
) error {
	c := launchctlCtl{m: newSpec.Mode}
	fs := laFS{m: newSpec.Mode}
	p := launchctlProber{m: newSpec.Mode}
	var rs rosterIO
	if newSpec.Workdir != "" {
		rs = newWorkdirRoster(newSpec.Workdir)
	}
	// FEATURE 25 (Element 2): after the swap, reap the OLD platform orphan
	// (bounded to the invoke mode) + sweep stale platform-workdirs. The new
	// platform PID is not known here (the new daemon starts it on its next tick),
	// so keepPID=0 = no PID exemption — the reaper kills same-mode orphans and the
	// new daemon self-heals its own platform if it was already up. NOT a
	// both-modes converge (that would break the transient two-generation swap).
	afterSwap := func() {
		home, _ := os.UserHomeDir()
		root := mode.SupportRoot(newSpec.Mode, home)
		_, _ = reapForeignPlatforms(root, 0, "", listPlatformProcs, resolvePlatformExecs, sig.VerifyFile, killProc)
		keepPW := platdir.Read(newSpec.Workdir) // survivor platform-workdir pointer
		_, _ = SweepStalePlatformWorkdirs(root, keepPW)
	}
	return SelfUpdate(cur, newSpec, newBin, c, fs, p, binPlacerFS{}, rs,
		healthyTimeout, probeInterval, keepOld, afterSwap)
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	i += len(a)
	j := strings.Index(s[i:], b)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(s[i : i+j])
}
