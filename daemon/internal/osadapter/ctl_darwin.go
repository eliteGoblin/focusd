//go:build darwin

package osadapter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

type launchctlCtl struct{}

func domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func (launchctlCtl) loaded(label string) bool {
	return exec.Command("launchctl", "print", domain()+"/"+label).Run() == nil
}
func (launchctlCtl) bootout(label string) error {
	return exec.Command("launchctl", "bootout", domain()+"/"+label).Run()
}
func (launchctlCtl) bootstrap(pp string) error {
	out, err := exec.Command("launchctl", "bootstrap", domain(), pp).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

type laFS struct{}

func (laFS) plistPath(label string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
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
	return installAll(s, launchctlCtl{}, laFS{})
}

// Uninstall boots out + removes all three entries.
func Uninstall(testMode bool) error {
	return uninstallAll(Spec{TestMode: testMode}, launchctlCtl{}, laFS{})
}

// EnsureAll recreates any missing mesh entry (mutual self-healing).
func EnsureAll(s Spec) ([]Role, error) {
	return ensureAll(s, launchctlCtl{}, laFS{})
}

// IsLoaded reports whether a role's launchd entry is registered.
func IsLoaded(testMode bool, r Role) bool {
	return launchctlCtl{}.loaded(LabelFor(testMode, r))
}

// UninstallProd removes a disguised prod install whose labels are
// randomized/unknown. It scans LaunchAgents and treats a plist as
// "ours" iff the binary it launches passes Ed25519 verification with
// the embedded public key (the design's signature recognition), then
// bootout + rm + pkill. Owner-driven teardown of a hidden install
// without needing to know the random names.
func UninstallProd() (removed []string, err error) {
	home, _ := os.UserHomeDir()
	laDir := filepath.Join(home, "Library", "LaunchAgents")
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
		_ = launchctlCtl{}.bootout(label)
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
