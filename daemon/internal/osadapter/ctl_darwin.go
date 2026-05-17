//go:build darwin

package osadapter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
