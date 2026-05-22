//go:build darwin

package osadapter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

// UninstallProd removes a disguised user/system install whose labels are
// randomized/unknown. It scans the launch dir for the current mode
// (resolved from euid: sudo → /Library/LaunchDaemons, else
// ~/Library/LaunchAgents) and treats a plist as "ours" iff the binary it
// launches passes Ed25519 verification with the embedded public key (the
// design's signature recognition), then bootout + rm + pkill. Owner-
// driven teardown of a hidden install without needing the random names.
func UninstallProd() (removed []string, err error) {
	m := mode.Resolve()
	home, _ := os.UserHomeDir()
	laDir := mode.LaunchDir(m, home)
	entries, rerr := os.ReadDir(laDir)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return nil, nil
		}
		return nil, rerr
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".plist") {
			continue
		}
		pp := filepath.Join(laDir, e.Name())
		label, bin := parsePlist(pp)
		if label == "" || bin == "" {
			continue
		}
		if ok, verr := sig.VerifyFile(bin); verr != nil || !ok {
			continue // not a genuine focusd binary → not ours
		}
		_ = exec.Command("pkill", "-f", bin).Run()
		_ = launchctlCtl{m: m}.bootout(label)
		_ = os.Remove(pp)
		removed = append(removed, label)
	}
	return removed, nil
}

// parsePlist extracts the Label and the first ProgramArguments string
// (the binary path) from one of our generated plists.
func parsePlist(path string) (label, bin string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	s := string(b)
	label = between(s, "<key>Label</key><string>", "</string>")
	if i := strings.Index(s, "<key>ProgramArguments</key><array>"); i >= 0 {
		bin = between(s[i:], "<string>", "</string>")
	}
	return label, bin
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
