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
	Roster     []string      // the 3 independent mesh labels (AllRoles order), from --roster argv
	Workdir    string        // recovered from the plist's --workdir argv
	BinaryPath string        // the ProgramArguments[0] binary path
	Interval   time.Duration // reconcile interval recovered from --interval argv (0 if absent)
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
// All three mesh entries must agree on the same binary path and the
// same --roster label set; otherwise the function returns whatever it
// could parse and the caller decides.
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
		// Establish the install's binary path + roster from the FIRST
		// matched plist; subsequent matches must agree on BOTH. The
		// roster (--roster argv) is the FEATURE 10 correlation key that
		// replaced the retired shared-base check: the three independent
		// labels share no stem, but every plist carries the same roster.
		if cur.BinaryPath == "" {
			cur.BinaryPath = bin
		} else if cur.BinaryPath != bin {
			continue // unrelated focusd install (different rotation)
		}
		roster := rosterFromArgv(argv)
		if cur.Roster == nil {
			cur.Roster = roster
		} else if !sameRoster(cur.Roster, roster) {
			continue // a different mesh's plist (different roster) → not ours
		}
		if cur.Workdir == "" {
			cur.Workdir = workdirFromArgv(argv)
		}
		if cur.Interval == 0 {
			cur.Interval = intervalFromArgv(argv)
		}
		cur.PlistPaths = append(cur.PlistPaths, pp)
		cur.Labels = append(cur.Labels, label)
	}
	return cur, nil
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

// rosterFromArgv pulls the comma-joined "--roster" value out of a parsed
// argv and splits it into the mesh-label set (FEATURE 10 / ADR-0014).
// Returns nil when the flag is absent or empty. This is the correlation
// key FindCurrentInstall uses now that the three labels share no base.
func rosterFromArgv(argv []string) []string {
	var raw string
	for i, a := range argv {
		if a == "--roster" && i+1 < len(argv) {
			raw = argv[i+1]
			break
		}
		if strings.HasPrefix(a, "--roster=") {
			raw = strings.TrimPrefix(a, "--roster=")
			break
		}
	}
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

// sameRoster reports whether two roster label sets are identical in
// order and content — the agreement check that ties three plists to one
// install.
func sameRoster(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// workdirFromArgv pulls the value following "--workdir" out of a parsed
// argv. Returns "" when the flag is absent.
func workdirFromArgv(argv []string) string {
	for i, a := range argv {
		if a == "--workdir" && i+1 < len(argv) {
			return argv[i+1]
		}
		if strings.HasPrefix(a, "--workdir=") {
			return strings.TrimPrefix(a, "--workdir=")
		}
	}
	return ""
}

// intervalFromArgv pulls the reconcile interval following "--interval"
// out of a parsed argv. Returns 0 when the flag is absent or the
// value doesn't parse — caller substitutes a default. Used by
// self-update to preserve the install-time interval across rotations
// instead of forcing the default on every update. (Copilot #6.)
func intervalFromArgv(argv []string) time.Duration {
	var raw string
	for i, a := range argv {
		if a == "--interval" && i+1 < len(argv) {
			raw = argv[i+1]
			break
		}
		if strings.HasPrefix(a, "--interval=") {
			raw = strings.TrimPrefix(a, "--interval=")
			break
		}
	}
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0
	}
	return d
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
