package infra

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockProcessManager and mockDaemonRegistry are defined in test_helpers_test.go

// TestCheckUpdate_NewerVersionAvailable verifies update detection
func TestCheckUpdate_NewerVersionAvailable(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "updater-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	pm := newMockProcessManager()
	registry := newMockDaemonRegistry()
	bm := &BackupManager{
		homeDir:    tmpDir,
		configPath: filepath.Join(tmpDir, ".config", ".helper.json"),
	}

	updater := &Updater{
		downloader: &GitHubDownloader{
			client: nil, // We'll override GetLatestVersion
		},
		backupManager:  bm,
		registry:       registry,
		pm:             pm,
		execMode:       &ExecModeConfig{Mode: "user", BinaryPath: filepath.Join(tmpDir, "appmon")},
		currentVersion: "0.1.0",
		logger:         zap.NewNop(),
	}

	// Test version comparison (used by CheckUpdate internally)
	tests := []struct {
		current  string
		latest   string
		expected bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.2.0", "0.2.0", false},
		{"0.2.0", "0.1.0", false},
		{"1.0.0", "2.0.0", true},
	}

	for _, tt := range tests {
		updater.currentVersion = tt.current
		available := isNewerVersion(tt.latest, tt.current)
		assert.Equal(t, tt.expected, available, "current=%s, latest=%s", tt.current, tt.latest)
	}
}

// TestStopDaemons_NoDaemonsRunning verifies stopping when no daemons exist
func TestStopDaemons_NoDaemonsRunning(t *testing.T) {
	pm := newMockProcessManager()
	registry := newMockDaemonRegistry()

	updater := &Updater{
		registry: registry,
		pm:       pm,
		logger:   zap.NewNop(),
	}

	// Should not error when no daemons registered
	err := updater.StopDaemons()
	assert.NoError(t, err)
}

// TestStopDaemons_DaemonsRunning verifies stopping running daemons
func TestStopDaemons_DaemonsRunning(t *testing.T) {
	pm := newMockProcessManager()
	pm.SetRunning(1001, true)
	pm.SetRunning(1002, true)

	registry := newMockDaemonRegistry()
	registry.setEntry(1001, 1002)

	updater := &Updater{
		registry: registry,
		pm:       pm,
		logger:   zap.NewNop(),
	}

	// Note: StopDaemons will return an error because signalProcess fails on fake PIDs
	// but the important thing is that it tried to kill them via the mock ProcessManager
	_ = updater.StopDaemons()

	// Verify daemons were killed via mock ProcessManager
	// The kill happens after 2s wait when SIGTERM fails
	assert.Contains(t, pm.killedPIDs, 1001, "watcher should be killed")
	assert.Contains(t, pm.killedPIDs, 1002, "guardian should be killed")
}

// TestVerifyDaemonsHealthy_BothRunning verifies health check passes
func TestVerifyDaemonsHealthy_BothRunning(t *testing.T) {
	pm := newMockProcessManager()
	pm.SetRunning(1001, true)
	pm.SetRunning(1002, true)

	registry := newMockDaemonRegistry()
	registry.setEntry(1001, 1002)

	updater := &Updater{
		registry: registry,
		pm:       pm,
		logger:   zap.NewNop(),
	}

	err := updater.VerifyDaemonsHealthy(2 * time.Second)
	assert.NoError(t, err)
}

// TestVerifyDaemonsHealthy_NeitherRunning verifies health check fails
func TestVerifyDaemonsHealthy_NeitherRunning(t *testing.T) {
	pm := newMockProcessManager()
	// No running PIDs

	registry := newMockDaemonRegistry()
	registry.setEntry(1001, 1002)

	updater := &Updater{
		registry: registry,
		pm:       pm,
		logger:   zap.NewNop(),
	}

	err := updater.VerifyDaemonsHealthy(500 * time.Millisecond)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "neither daemon running")
}

// TestVerifyDaemonsHealthy_OnlyWatcherRunning verifies partial failure
func TestVerifyDaemonsHealthy_OnlyWatcherRunning(t *testing.T) {
	pm := newMockProcessManager()
	pm.SetRunning(1001, true) // Only watcher

	registry := newMockDaemonRegistry()
	registry.setEntry(1001, 1002)

	updater := &Updater{
		registry: registry,
		pm:       pm,
		logger:   zap.NewNop(),
	}

	err := updater.VerifyDaemonsHealthy(500 * time.Millisecond)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "guardian not running")
}

// TestVerifyDaemonsHealthy_NoRegistry verifies handling of missing registry
func TestVerifyDaemonsHealthy_NoRegistry(t *testing.T) {
	pm := newMockProcessManager()
	registry := newMockDaemonRegistry()
	// No entry set

	updater := &Updater{
		registry: registry,
		pm:       pm,
		logger:   zap.NewNop(),
	}

	err := updater.VerifyDaemonsHealthy(500 * time.Millisecond)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no daemons registered")
}

// TestCreateRollbackBackup verifies backup creation
func TestCreateRollbackBackup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "updater-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create a fake binary
	binaryPath := filepath.Join(tmpDir, "appmon")
	content := []byte("fake binary content")
	err = os.WriteFile(binaryPath, content, 0755)
	require.NoError(t, err)

	updater := &Updater{logger: zap.NewNop()}

	rollbackPath, err := updater.createRollbackBackup(binaryPath)
	require.NoError(t, err)
	defer os.RemoveAll(filepath.Dir(rollbackPath))

	// Verify backup exists and has correct content
	backupContent, err := os.ReadFile(rollbackPath)
	require.NoError(t, err)
	assert.Equal(t, content, backupContent)
}

// TestUpdater_Constants verifies timeout constants are reasonable
func TestUpdater_Constants(t *testing.T) {
	assert.Equal(t, 10*time.Second, DefaultHealthCheckTimeout,
		"health check timeout should be 10 seconds")
	assert.Equal(t, 500*time.Millisecond, DaemonCheckInterval,
		"daemon check interval should be 500ms")
}

// TestNewUpdater_CreatesValidInstance verifies constructor
func TestNewUpdater_CreatesValidInstance(t *testing.T) {
	logger := zap.NewNop()
	updater := NewUpdater("1.0.0", logger)

	assert.NotNil(t, updater.downloader)
	assert.NotNil(t, updater.backupManager)
	assert.NotNil(t, updater.registry)
	assert.NotNil(t, updater.pm)
	assert.NotNil(t, updater.execMode)
	assert.Equal(t, "1.0.0", updater.currentVersion)
	assert.Equal(t, logger, updater.logger)
}

// TestNewUpdaterWithDeps_AllowsDependencyInjection verifies DI constructor
func TestNewUpdaterWithDeps_AllowsDependencyInjection(t *testing.T) {
	downloader := NewGitHubDownloader()
	bm := NewBackupManager()
	pm := newMockProcessManager()
	registry := newMockDaemonRegistry()
	execMode := &ExecModeConfig{Mode: "user"}
	logger := zap.NewNop()

	updater := NewUpdaterWithDeps(downloader, bm, registry, pm, execMode, "2.0.0", logger)

	assert.Equal(t, downloader, updater.downloader)
	assert.Equal(t, bm, updater.backupManager)
	assert.Equal(t, registry, updater.registry)
	assert.Equal(t, pm, updater.pm)
	assert.Equal(t, execMode, updater.execMode)
	assert.Equal(t, "2.0.0", updater.currentVersion)
}

// TestUpdateResult_Defaults verifies UpdateResult zero values
func TestUpdateResult_Defaults(t *testing.T) {
	result := &UpdateResult{}

	assert.False(t, result.Success)
	assert.False(t, result.RolledBack)
	assert.Empty(t, result.PreviousVer)
	assert.Empty(t, result.NewVer)
	assert.Empty(t, result.RollbackReason)
}

// TestRollback_RestoresBinary verifies rollback restores the binary
func TestRollback_RestoresBinary(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "updater-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create original binary
	binaryPath := filepath.Join(tmpDir, "appmon")
	originalContent := []byte("original binary")
	err = os.WriteFile(binaryPath, originalContent, 0755)
	require.NoError(t, err)

	// Create rollback backup
	rollbackDir, err := os.MkdirTemp("", "rollback-")
	require.NoError(t, err)
	defer os.RemoveAll(rollbackDir)
	rollbackPath := filepath.Join(rollbackDir, "appmon-rollback")
	err = os.WriteFile(rollbackPath, originalContent, 0755)
	require.NoError(t, err)

	// Modify binary (simulate failed update)
	err = os.WriteFile(binaryPath, []byte("corrupted"), 0755)
	require.NoError(t, err)

	pm := newMockProcessManager()
	registry := newMockDaemonRegistry()
	bm := &BackupManager{
		homeDir:    tmpDir,
		configPath: filepath.Join(tmpDir, ".config", ".helper.json"),
	}

	updater := &Updater{
		backupManager:  bm,
		registry:       registry,
		pm:             pm,
		execMode:       &ExecModeConfig{Mode: "user", BinaryPath: binaryPath},
		currentVersion: "1.0.0",
		logger:         zap.NewNop(),
	}

	// Perform rollback - will fail at StartDaemons but binary should be restored
	_ = updater.rollback(rollbackPath, binaryPath)

	// Verify binary was restored
	restoredContent, err := os.ReadFile(binaryPath)
	require.NoError(t, err)
	assert.Equal(t, originalContent, restoredContent)
}

// TestSignalProcess_InvalidPID verifies error handling for invalid PID
func TestSignalProcess_InvalidPID(t *testing.T) {
	updater := &Updater{logger: zap.NewNop()}

	// PID -1 is invalid
	err := updater.signalProcess(-1, 0)
	assert.Error(t, err)
}

// TestLog_NilLogger verifies log doesn't panic with nil logger
func TestLog_NilLogger(t *testing.T) {
	updater := &Updater{logger: nil}

	// Should not panic
	assert.NotPanics(t, func() {
		updater.log("test message")
	})
}

// TestLog_WithLogger verifies log works with logger
func TestLog_WithLogger(t *testing.T) {
	updater := &Updater{logger: zap.NewNop()}

	// Should not panic
	assert.NotPanics(t, func() {
		updater.log("test message", zap.String("key", "value"))
	})
}
