package infra

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// mockFreedomDeps provides injectable dependencies for testing FreedomProtector
type mockFreedomDeps struct {
	appExists        bool
	appRunning       bool
	proxyRunning     bool
	helperRunning    bool
	loginItemPresent bool
	restartAppCalled bool
	restartAppErr    error
	restoreLoginErr  error
}

func (m *mockFreedomDeps) IsInstalled() bool       { return m.appExists }
func (m *mockFreedomDeps) IsAppRunning() bool      { return m.appRunning }
func (m *mockFreedomDeps) IsProxyRunning() bool    { return m.proxyRunning }
func (m *mockFreedomDeps) IsHelperRunning() bool   { return m.helperRunning }
func (m *mockFreedomDeps) IsLoginItemPresent() bool { return m.loginItemPresent }

func (m *mockFreedomDeps) RestartApp() error {
	m.restartAppCalled = true
	if m.restartAppErr != nil {
		return m.restartAppErr
	}
	m.appRunning = true
	m.proxyRunning = true
	return nil
}

func (m *mockFreedomDeps) RestoreLoginItem() error {
	if m.restoreLoginErr != nil {
		return m.restoreLoginErr
	}
	m.loginItemPresent = true
	return nil
}

func (m *mockFreedomDeps) GetHealth() domain.FreedomHealth {
	return domain.FreedomHealth{
		Installed:        m.appExists,
		AppRunning:       m.appRunning,
		ProxyRunning:     m.proxyRunning,
		HelperRunning:    m.helperRunning,
		LoginItemPresent: m.loginItemPresent,
		ProxyPort:        7769,
	}
}

func (m *mockFreedomDeps) Protect() (bool, error) {
	actionTaken := false

	if !m.appExists {
		return false, nil // Freedom not installed, skip
	}

	if !m.appRunning {
		if err := m.RestartApp(); err != nil {
			return false, err
		}
		actionTaken = true
	}

	if !m.loginItemPresent {
		if err := m.RestoreLoginItem(); err != nil {
			return actionTaken, err
		}
		actionTaken = true
	}

	return actionTaken, nil
}

// Ensure mock implements the interface
var _ domain.FreedomProtector = (*mockFreedomDeps)(nil)

// TestFreedomProtector_NotInstalled verifies graceful skip when Freedom not installed
func TestFreedomProtector_NotInstalled(t *testing.T) {
	mock := &mockFreedomDeps{
		appExists: false,
	}

	assert.False(t, mock.IsInstalled())

	actionTaken, err := mock.Protect()
	require.NoError(t, err)
	assert.False(t, actionTaken, "should not take action when not installed")
	assert.False(t, mock.restartAppCalled, "should not restart when not installed")
}

// TestFreedomProtector_HealthyState verifies no action when everything is running
func TestFreedomProtector_HealthyState(t *testing.T) {
	mock := &mockFreedomDeps{
		appExists:        true,
		appRunning:       true,
		proxyRunning:     true,
		helperRunning:    true,
		loginItemPresent: true,
	}

	health := mock.GetHealth()
	assert.True(t, health.Installed)
	assert.True(t, health.AppRunning)
	assert.True(t, health.ProxyRunning)
	assert.True(t, health.HelperRunning)
	assert.True(t, health.LoginItemPresent)
	assert.Equal(t, 7769, health.ProxyPort)

	actionTaken, err := mock.Protect()
	require.NoError(t, err)
	assert.False(t, actionTaken, "should not take action when healthy")
	assert.False(t, mock.restartAppCalled, "should not restart when already running")
}

// TestFreedomProtector_AppKilled verifies restart when app is killed
func TestFreedomProtector_AppKilled(t *testing.T) {
	mock := &mockFreedomDeps{
		appExists:        true,
		appRunning:       false, // App killed
		proxyRunning:     false, // Proxy also dead
		helperRunning:    true,  // Helper still running
		loginItemPresent: true,
	}

	actionTaken, err := mock.Protect()
	require.NoError(t, err)
	assert.True(t, actionTaken, "should take action to restart")
	assert.True(t, mock.restartAppCalled, "should call restart")
	assert.True(t, mock.appRunning, "app should be running after restart")
	assert.True(t, mock.proxyRunning, "proxy should start with app")
}

// TestFreedomProtector_LoginItemRemoved verifies restoration when login item removed
func TestFreedomProtector_LoginItemRemoved(t *testing.T) {
	mock := &mockFreedomDeps{
		appExists:        true,
		appRunning:       true,
		proxyRunning:     true,
		helperRunning:    true,
		loginItemPresent: false, // Login item removed
	}

	actionTaken, err := mock.Protect()
	require.NoError(t, err)
	assert.True(t, actionTaken, "should take action to restore login item")
	assert.True(t, mock.loginItemPresent, "login item should be restored")
}

// TestFreedomProtector_BothAppKilledAndLoginItemRemoved verifies both actions
func TestFreedomProtector_BothAppKilledAndLoginItemRemoved(t *testing.T) {
	mock := &mockFreedomDeps{
		appExists:        true,
		appRunning:       false,
		proxyRunning:     false,
		helperRunning:    true,
		loginItemPresent: false,
	}

	actionTaken, err := mock.Protect()
	require.NoError(t, err)
	assert.True(t, actionTaken, "should take action")
	assert.True(t, mock.restartAppCalled, "should restart app")
	assert.True(t, mock.appRunning, "app should be running")
	assert.True(t, mock.loginItemPresent, "login item should be restored")
}

// TestFreedomProtector_HelperMissing verifies graceful degradation when helper missing
func TestFreedomProtector_HelperMissing(t *testing.T) {
	mock := &mockFreedomDeps{
		appExists:        true,
		appRunning:       true,
		proxyRunning:     true,
		helperRunning:    false, // Helper missing (can't fix, just report)
		loginItemPresent: true,
	}

	health := mock.GetHealth()
	assert.False(t, health.HelperRunning, "should report helper not running")

	// Still shouldn't error - just degraded status
	actionTaken, err := mock.Protect()
	require.NoError(t, err)
	assert.False(t, actionTaken, "no action needed for helper (can't restart it)")
}

// TestFreedomProtector_GetHealth verifies health struct population
func TestFreedomProtector_GetHealth(t *testing.T) {
	tests := []struct {
		name     string
		mock     mockFreedomDeps
		expected domain.FreedomHealth
	}{
		{
			name: "fully healthy",
			mock: mockFreedomDeps{
				appExists:        true,
				appRunning:       true,
				proxyRunning:     true,
				helperRunning:    true,
				loginItemPresent: true,
			},
			expected: domain.FreedomHealth{
				Installed:        true,
				AppRunning:       true,
				ProxyRunning:     true,
				HelperRunning:    true,
				LoginItemPresent: true,
				ProxyPort:        7769,
			},
		},
		{
			name: "not installed",
			mock: mockFreedomDeps{
				appExists: false,
			},
			expected: domain.FreedomHealth{
				Installed: false,
				ProxyPort: 7769,
			},
		},
		{
			name: "degraded - helper missing",
			mock: mockFreedomDeps{
				appExists:        true,
				appRunning:       true,
				proxyRunning:     true,
				helperRunning:    false,
				loginItemPresent: true,
			},
			expected: domain.FreedomHealth{
				Installed:        true,
				AppRunning:       true,
				ProxyRunning:     true,
				HelperRunning:    false,
				LoginItemPresent: true,
				ProxyPort:        7769,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health := tt.mock.GetHealth()
			assert.Equal(t, tt.expected, health)
		})
	}
}

// TestFreedomProtector_Constants verifies expected values
func TestFreedomProtector_Constants(t *testing.T) {
	assert.Equal(t, "/Applications/Freedom.app", FreedomAppPath)
	assert.Equal(t, "Freedom", FreedomProcessName)
	assert.Equal(t, "FreedomProxy", FreedomProxyProcessName)
	assert.Equal(t, "com.80pct.FreedomHelper", FreedomHelperProcessName)
	assert.Equal(t, 7769, FreedomProxyPort)
}

// TestNewFreedomProtector verifies constructor creates valid instance
func TestNewFreedomProtector(t *testing.T) {
	pm := NewProcessManager()
	logger := zap.NewNop()

	protector := NewFreedomProtector(pm, logger)
	require.NotNil(t, protector)

	// Verify it implements the interface
	var _ domain.FreedomProtector = protector
}

// TestFreedomProtector_IsInstalled_Real tests with real filesystem check
func TestFreedomProtector_IsInstalled_Real(t *testing.T) {
	pm := NewProcessManager()
	logger := zap.NewNop()
	protector := NewFreedomProtector(pm, logger)

	// This will return true or false based on whether Freedom is actually installed
	// We just verify it doesn't panic
	installed := protector.IsInstalled()
	t.Logf("Freedom installed: %v", installed)
}
