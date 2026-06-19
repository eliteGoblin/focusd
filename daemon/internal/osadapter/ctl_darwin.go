//go:build darwin

package osadapter

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
	"github.com/eliteGoblin/focusd/daemon/internal/mode"
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
	// lastArgv keeps the argv of the most recently matched plist so the
	// post-loop workdir/roster recovery can fall back to OLD-plist argv
	// flags (--workdir/--roster) when the masked file / Dir(bin) path
	// isn't available. All three mesh plists carry the same values, so any
	// one of them is a valid fallback source.
	var lastArgv []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".plist") {
			continue
		}
		pp := filepath.Join(laDir, e.Name())
		label, bin, argv := parsePlist(pp)
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
		cur.Roster = recoverRoster(cur.Workdir, lastArgv)
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
func recoverRoster(workdir string, argv []string) []string {
	if workdir != "" {
		if labels, err := core.ReadRoster((&core.Store{Dir: workdir}).RosterPath()); err == nil {
			return labels
		}
	}
	return rosterFromArgv(argv)
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

// parsePlist extracts the Label, the first ProgramArguments string
// (the binary path) and the full argv (binary path + arguments) from
// one of our generated plists. argv[0] == bin; len(argv) == 0 on parse
// failure.
func parsePlist(path string) (label, bin string, argv []string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", nil
	}
	s := string(b)
	label = between(s, "<key>Label</key><string>", "</string>")
	i := strings.Index(s, "<key>ProgramArguments</key><array>")
	if i < 0 {
		return label, "", nil
	}
	// Walk the inner <string>...</string> entries up to the closing
	// </array>; preserves order and handles the "--flag value" pair
	// form the daemon emits.
	tail := s[i+len("<key>ProgramArguments</key><array>"):]
	end := strings.Index(tail, "</array>")
	if end < 0 {
		return label, "", nil
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
	if len(argv) > 0 {
		bin = argv[0]
	}
	return label, bin, argv
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
	return SelfUpdate(cur, newSpec, newBin, c, fs, p, binPlacerFS{}, rs,
		healthyTimeout, probeInterval, keepOld)
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
