package infra

import (
	"bytes"
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

// Install creates and loads the plist (LaunchAgent or LaunchDaemon).
func (m *LaunchdManagerImpl) Install(execPath string) error {
	// Ensure plist directory exists
	if err := os.MkdirAll(m.plistDir, 0755); err != nil {
		return err
	}

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
		return err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, config); err != nil {
		return err
	}

	// Write plist file
	if err := os.WriteFile(m.plistPath, buf.Bytes(), 0644); err != nil {
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

// GetPlistPath returns the plist file path.
func (m *LaunchdManagerImpl) GetPlistPath() string {
	return m.plistPath
}

// load loads the plist using launchctl.
func (m *LaunchdManagerImpl) load() error {
	if m.mode == ExecModeSystem {
		// LaunchDaemon: use bootstrap for system domain
		cmd := exec.Command("launchctl", "load", m.plistPath)
		return cmd.Run()
	}

	// LaunchAgent: load for current user
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
