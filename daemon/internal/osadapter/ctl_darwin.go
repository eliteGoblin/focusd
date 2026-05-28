//go:build darwin

package osadapter

import (
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

// Install writes + loads all three mesh entries.
func Install(s Spec) error {
	_ = os.MkdirAll(s.Workdir, 0o755)
	return installAll(s, launchctlCtl{m: s.Mode}, laFS{m: s.Mode})
}

// Uninstall boots out + removes all three entries.
func Uninstall(testMode bool) error {
	m := modeFromTestFlag(testMode)
	return uninstallAll(Spec{Mode: m}, launchctlCtl{m: m}, laFS{m: m})
}

// EnsureAll recreates any missing mesh entry (mutual self-healing).
func EnsureAll(s Spec) ([]Role, error) {
	return ensureAll(s, launchctlCtl{m: s.Mode}, laFS{m: s.Mode})
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
// whose Label shares the same disguised base AND whose ProgramArguments
// point at the same Ed25519-verified binary). When the mesh is
// incomplete the entries that were found are still returned in the
// slices in scan order (caller decides whether that is fatal).
type CurInstall struct {
	Mode       mode.Mode // user | system (Test is not discovered this way)
	Base       string    // disguised label base, e.g. "com.apple.metadata.helper.7f3a"
	Workdir    string    // recovered from the plist's --workdir argv
	BinaryPath string    // the ProgramArguments[0] binary path
	PlistPaths []string  // up to 3, in scan order
	Labels     []string  // up to 3, in scan order (aligned with PlistPaths)
}

// FindCurrentInstall scans the LaunchDir for the given mode and returns
// the focusd install rooted there (if any). A plist is treated as ours
// only when ProgramArguments[0] passes Ed25519 verification with the
// embedded public key — the design's signature recognition (see
// daemon_design.md §6). Returns (zero, nil) when no genuine install is
// found, an error only for filesystem failure.
//
// All three mesh entries must agree on the same binary path and the
// same disguised label base; otherwise the function returns whatever
// it could parse and the caller decides.
// verifyFn is the verifier seam — production uses sig.VerifyFile (the
// real Ed25519 check against the embedded public key); tests override
// it to avoid needing the offline private key in CI.
var verifyFn = sig.VerifyFile

func FindCurrentInstall(m mode.Mode) (CurInstall, error) {
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
		if ok, verr := verifyFn(bin); verr != nil || !ok {
			continue // not a genuine focusd binary → not ours
		}
		// Establish the install's binary path + base from the FIRST
		// matched plist; subsequent matches must agree.
		if cur.BinaryPath == "" {
			cur.BinaryPath = bin
		} else if cur.BinaryPath != bin {
			continue // unrelated focusd install (different rotation)
		}
		base := labelBase(label)
		if cur.Base == "" {
			cur.Base = base
		} else if cur.Base != base {
			continue
		}
		if cur.Workdir == "" {
			cur.Workdir = workdirFromArgv(argv)
		}
		cur.PlistPaths = append(cur.PlistPaths, pp)
		cur.Labels = append(cur.Labels, label)
	}
	return cur, nil
}

// labelBase strips a trailing ".a"/".b"/".ensure" suffix off a launchd
// label, returning the disguised install base.
func labelBase(label string) string {
	for _, suf := range []string{".a", ".b", ".ensure"} {
		if strings.HasSuffix(label, suf) {
			return strings.TrimSuffix(label, suf)
		}
	}
	return label
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

// UninstallProd removes a disguised user/system install whose labels are
// randomized/unknown. It uses FindCurrentInstall for the scan, then
// bootouts + removes plists + pkills the binary. Owner-driven teardown
// of a hidden install without needing the random names.
func UninstallProd() (removed []string, err error) {
	m := mode.Resolve()
	cur, ferr := FindCurrentInstall(m)
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
	if _, err := io.Copy(out, byteReader(srcBytes)); err != nil {
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

// byteReader is a tiny io.Reader over a []byte without pulling in
// bytes.NewReader at the call site — kept inline so binPlacer stays
// stdlib-shaped and dep-free.
type byteReader []byte

func (b byteReader) Read(p []byte) (int, error) {
	if len(b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b)
	return n, nil
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
	return SelfUpdate(cur, newSpec, newBin, c, fs, p, binPlacerFS{},
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
