package daemon

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/eliteGoblin/focusd/app_mon/internal/policy"
)

// TestDefaultWatcherConfig verifies default watcher configuration
func TestDefaultWatcherConfig(t *testing.T) {
	config := DefaultWatcherConfig()

	assert.Equal(t, policy.DefaultScanInterval, config.EnforcementInterval)
	assert.Equal(t, 30*time.Second, config.HeartbeatInterval)
	assert.Equal(t, 60*time.Second, config.PartnerCheckInterval)
	assert.Equal(t, 60*time.Second, config.PlistCheckInterval)
	assert.Equal(t, 60*time.Second, config.BinaryCheckInterval)
}

// TestDefaultGuardianConfig verifies default guardian configuration
func TestDefaultGuardianConfig(t *testing.T) {
	config := DefaultGuardianConfig()

	assert.Equal(t, 30*time.Second, config.WatcherCheckInterval)
	assert.Equal(t, 30*time.Second, config.HeartbeatInterval)
}

// TestWatcherConfig_AllFieldsSet verifies all watcher config fields have values
func TestWatcherConfig_AllFieldsSet(t *testing.T) {
	config := DefaultWatcherConfig()

	assert.NotZero(t, config.EnforcementInterval, "EnforcementInterval should be set")
	assert.NotZero(t, config.HeartbeatInterval, "HeartbeatInterval should be set")
	assert.NotZero(t, config.PartnerCheckInterval, "PartnerCheckInterval should be set")
	assert.NotZero(t, config.PlistCheckInterval, "PlistCheckInterval should be set")
	assert.NotZero(t, config.BinaryCheckInterval, "BinaryCheckInterval should be set")
}

// TestGuardianConfig_AllFieldsSet verifies all guardian config fields have values
func TestGuardianConfig_AllFieldsSet(t *testing.T) {
	config := DefaultGuardianConfig()

	assert.NotZero(t, config.WatcherCheckInterval, "WatcherCheckInterval should be set")
	assert.NotZero(t, config.HeartbeatInterval, "HeartbeatInterval should be set")
}

// TestBackupManagerInterface verifies the BackupManager interface exists
func TestBackupManagerInterface(t *testing.T) {
	// This test just verifies the interface is defined correctly
	// by using a mock that implements it
	var _ BackupManager = &mockBackupManager{}
}

type mockBackupManager struct{}

func (m *mockBackupManager) VerifyAndRestore() (bool, error) {
	return false, nil
}

func (m *mockBackupManager) GetMainBinaryPath() string {
	return "/test/path/appmon"
}
