//go:build darwin

package osadapter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

func plistPath(label string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

func domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// Install writes a LaunchAgent plist (KeepAlive + RunAtLoad — survives
// kill/crash/reboot-at-login) and bootstraps it. Idempotent: an existing
// agent with the same label is booted out first.
func Install(s Spec) error {
	if s.SelfPath == "" {
		return fmt.Errorf("osadapter: empty SelfPath")
	}
	label := s.Label()
	pp := plistPath(label)
	if err := os.MkdirAll(filepath.Dir(pp), 0o755); err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array>
    <string>%s</string><string>run</string>
    <string>--workdir</string><string>%s</string>
    <string>--github</string><string>%s</string>
    <string>--asset</string><string>%s</string>
    <string>--interval</string><string>%s</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>StandardErrorPath</key><string>%s/daemon.log</string>
  <key>StandardOutPath</key><string>%s/daemon.log</string>
</dict></plist>
`, label, s.SelfPath, s.Workdir, s.Github, s.Asset, s.Interval.String(),
		s.Workdir, s.Workdir)

	_ = os.MkdirAll(s.Workdir, 0o755)
	if err := os.WriteFile(pp, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("osadapter: write plist: %w", err)
	}
	// Idempotent: bootout any stale instance, then bootstrap.
	_ = exec.Command("launchctl", "bootout", domain()+"/"+label).Run()
	if out, err := exec.Command("launchctl", "bootstrap", domain(), pp).CombinedOutput(); err != nil {
		return fmt.Errorf("osadapter: bootstrap: %w (%s)", err, out)
	}
	return nil
}

// Uninstall boots out the agent and removes its plist (idempotent).
func Uninstall(testMode bool) error {
	label := LabelFor(testMode)
	_ = exec.Command("launchctl", "bootout", domain()+"/"+label).Run()
	if err := os.Remove(plistPath(label)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("osadapter: remove plist: %w", err)
	}
	return nil
}

// IsLoaded reports whether the agent is currently registered with launchd.
func IsLoaded(testMode bool) bool {
	label := LabelFor(testMode)
	return exec.Command("launchctl", "print", domain()+"/"+label).Run() == nil
}
