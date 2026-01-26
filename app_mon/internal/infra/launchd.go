package infra

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"

	"github.com/user/focusd/app_mon/internal/domain"
)

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
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

// LaunchAgentManagerImpl implements domain.LaunchAgentManager.
type LaunchAgentManagerImpl struct {
	launchAgentsDir string
	plistPath       string
}

// NewLaunchAgentManager creates a new LaunchAgent manager.
func NewLaunchAgentManager() domain.LaunchAgentManager {
	home, _ := os.UserHomeDir()
	launchAgentsDir := filepath.Join(home, "Library/LaunchAgents")
	plistPath := filepath.Join(launchAgentsDir, launchAgentLabel+".plist")

	return &LaunchAgentManagerImpl{
		launchAgentsDir: launchAgentsDir,
		plistPath:       plistPath,
	}
}

// Install creates and loads the LaunchAgent plist.
func (m *LaunchAgentManagerImpl) Install(execPath string) error {
	// Ensure LaunchAgents directory exists
	if err := os.MkdirAll(m.launchAgentsDir, 0755); err != nil {
		return err
	}

	// Generate plist content
	config := plistConfig{
		Label:          launchAgentLabel,
		ExecutablePath: execPath,
		LogPath:        filepath.Join(logDir, "appmon.log"),
		ErrorLogPath:   filepath.Join(logDir, "appmon.error.log"),
	}

	tmpl, err := template.New("plist").Parse(plistTemplate)
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

	// Load the LaunchAgent
	return m.load()
}

// Uninstall unloads and removes the LaunchAgent plist.
func (m *LaunchAgentManagerImpl) Uninstall() error {
	// Unload first (ignore errors if not loaded)
	_ = m.unload()

	// Remove plist file
	return os.Remove(m.plistPath)
}

// IsInstalled checks if LaunchAgent is installed.
func (m *LaunchAgentManagerImpl) IsInstalled() bool {
	_, err := os.Stat(m.plistPath)
	return err == nil
}

// GetPlistPath returns the plist file path.
func (m *LaunchAgentManagerImpl) GetPlistPath() string {
	return m.plistPath
}

// load loads the LaunchAgent using launchctl.
func (m *LaunchAgentManagerImpl) load() error {
	cmd := exec.Command("launchctl", "load", m.plistPath)
	return cmd.Run()
}

// unload unloads the LaunchAgent using launchctl.
func (m *LaunchAgentManagerImpl) unload() error {
	cmd := exec.Command("launchctl", "unload", m.plistPath)
	return cmd.Run()
}

// Ensure LaunchAgentManagerImpl implements domain.LaunchAgentManager.
var _ domain.LaunchAgentManager = (*LaunchAgentManagerImpl)(nil)
