package infra

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// LaunchAgent plist template (runs as user)
const launchAgentTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>

    <key>ProgramArguments</key>
    <array>
        <string>{{.ExecutablePath}}</string>
        <string>start</string>
    </array>

    <key>RunAtLoad</key>
    <true/>

    <key>KeepAlive</key>
    <dict>
        <key>Crashed</key>
        <true/>
    </dict>

    <key>StandardOutPath</key>
    <string>{{.LogPath}}</string>

    <key>StandardErrorPath</key>
    <string>{{.ErrorLogPath}}</string>

    <key>ProcessType</key>
    <string>Background</string>

    <key>ThrottleInterval</key>
    <integer>10</integer>
</dict>
</plist>`

// LaunchDaemon plist template (runs as root)
const launchDaemonTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>

    <key>ProgramArguments</key>
    <array>
        <string>{{.ExecutablePath}}</string>
        <string>start</string>
    </array>

    <key>RunAtLoad</key>
    <true/>

    <key>KeepAlive</key>
    <true/>

    <key>StandardOutPath</key>
    <string>{{.LogPath}}</string>

    <key>StandardErrorPath</key>
    <string>{{.ErrorLogPath}}</string>

    <key>ThrottleInterval</key>
    <integer>10</integer>
</dict>
</plist>`

const (
	launchAgentLabel = "com.focusd.appmon"
	logDir           = "/var/tmp"
)

type plistConfig struct {
	Label          string
	ExecutablePath string
	LogPath        string
	ErrorLogPath   string
}

// LaunchdManagerImpl implements domain.LaunchAgentManager for both modes.
type LaunchdManagerImpl struct {
	mode      ExecMode
	plistDir  string
	plistPath string
}

// NewLaunchAgentManager creates a new LaunchAgent manager (user mode).
func NewLaunchAgentManager() domain.LaunchAgentManager {
	home, _ := os.UserHomeDir()
	launchAgentsDir := filepath.Join(home, "Library/LaunchAgents")
	plistPath := filepath.Join(launchAgentsDir, launchAgentLabel+".plist")

	return &LaunchdManagerImpl{
		mode:      ExecModeUser,
		plistDir:  launchAgentsDir,
		plistPath: plistPath,
	}
}

// NewLaunchdManager creates a launchd manager based on execution mode.
func NewLaunchdManager(config *ExecModeConfig) domain.LaunchAgentManager {
	return &LaunchdManagerImpl{
		mode:      config.Mode,
		plistDir:  config.PlistDir,
		plistPath: config.PlistPath,
	}
}

// generatePlistContent creates plist content for the given exec path.
func (m *LaunchdManagerImpl) generatePlistContent(execPath string) []byte {
	// Select template based on mode
	var tmplStr string
	if m.mode == ExecModeSystem {
		tmplStr = launchDaemonTemplate
	} else {
		tmplStr = launchAgentTemplate
	}

	// Generate plist content
	config := plistConfig{
		Label:          launchAgentLabel,
		ExecutablePath: execPath,
		LogPath:        filepath.Join(logDir, "appmon.log"),
		ErrorLogPath:   filepath.Join(logDir, "appmon.error.log"),
	}

	tmpl, err := template.New("plist").Parse(tmplStr)
	if err != nil {
		return nil
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, config); err != nil {
		return nil
	}

	return buf.Bytes()
}

// Install creates and loads the plist (LaunchAgent or LaunchDaemon).
func (m *LaunchdManagerImpl) Install(execPath string) error {
	// Ensure plist directory exists
	if err := os.MkdirAll(m.plistDir, 0755); err != nil {
		return err
	}

	// Generate plist content
	content := m.generatePlistContent(execPath)
	if content == nil {
		return fmt.Errorf("failed to generate plist content")
	}

	// Write plist file
	if err := os.WriteFile(m.plistPath, content, 0644); err != nil {
		return err
	}

	// Load the plist
	return m.load()
}

// Uninstall unloads and removes the plist.
func (m *LaunchdManagerImpl) Uninstall() error {
	// Unload first (ignore errors if not loaded)
	_ = m.unload()

	// Remove plist file
	return os.Remove(m.plistPath)
}

// IsInstalled checks if plist is installed.
func (m *LaunchdManagerImpl) IsInstalled() bool {
	_, err := os.Stat(m.plistPath)
	return err == nil
}

// NeedsUpdate checks if plist exists but has different content than expected.
func (m *LaunchdManagerImpl) NeedsUpdate(execPath string) bool {
	if !m.IsInstalled() {
		return false // Doesn't exist, needs install not update
	}

	// Read current plist
	currentContent, err := os.ReadFile(m.plistPath)
	if err != nil {
		return true // Can't read, assume needs update
	}

	// Generate expected content
	expectedContent := m.generatePlistContent(execPath)
	if expectedContent == nil {
		return true // Can't generate, assume needs update
	}

	return !bytes.Equal(currentContent, expectedContent)
}

// Update unloads, updates plist content, and reloads.
func (m *LaunchdManagerImpl) Update(execPath string) error {
	// Unload first (ignore errors if not loaded)
	_ = m.unload()

	// Generate and write new plist content
	content := m.generatePlistContent(execPath)
	if content == nil {
		return fmt.Errorf("failed to generate plist content")
	}

	if err := os.WriteFile(m.plistPath, content, 0644); err != nil {
		return err
	}

	// Reload
	return m.load()
}

// CleanupOtherMode removes plist from the other mode location if it exists.
// This handles mode migration (user↔system) by cleaning up stale configs.
func (m *LaunchdManagerImpl) CleanupOtherMode() error {
	var otherPath string
	if m.mode == ExecModeUser {
		// We're user mode, cleanup system mode if exists
		otherPath = "/Library/LaunchDaemons/" + launchAgentLabel + ".plist"
	} else {
		// We're system mode, cleanup user mode if exists
		home, _ := os.UserHomeDir()
		otherPath = filepath.Join(home, "Library/LaunchAgents", launchAgentLabel+".plist")
	}

	if _, err := os.Stat(otherPath); err == nil {
		// Other mode plist exists - unload and remove
		_ = exec.Command("launchctl", "unload", otherPath).Run()
		return os.Remove(otherPath)
	}
	return nil
}

// GetPlistPath returns the plist file path.
func (m *LaunchdManagerImpl) GetPlistPath() string {
	return m.plistPath
}

// load loads the plist using launchctl.
// Note: `launchctl load` is deprecated but still works on macOS.
// Modern approach would use `launchctl bootstrap` for system domain
// and `launchctl bootstrap gui/<uid>` for user domain, but `load`
// is simpler and sufficient for this use case.
func (m *LaunchdManagerImpl) load() error {
	cmd := exec.Command("launchctl", "load", m.plistPath)
	return cmd.Run()
}

// unload unloads the plist using launchctl.
func (m *LaunchdManagerImpl) unload() error {
	cmd := exec.Command("launchctl", "unload", m.plistPath)
	return cmd.Run()
}

// GetMode returns the current execution mode.
func (m *LaunchdManagerImpl) GetMode() ExecMode {
	return m.mode
}

// Ensure LaunchdManagerImpl implements domain.LaunchAgentManager.
var _ domain.LaunchAgentManager = (*LaunchdManagerImpl)(nil)
