package infra

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		stored   string
		expected bool
	}{
		{
			name:     "newer major version",
			current:  "1.0.0",
			stored:   "0.9.9",
			expected: true,
		},
		{
			name:     "newer minor version",
			current:  "0.2.0",
			stored:   "0.1.0",
			expected: true,
		},
		{
			name:     "newer patch version",
			current:  "0.1.1",
			stored:   "0.1.0",
			expected: true,
		},
		{
			name:     "equal versions",
			current:  "0.1.0",
			stored:   "0.1.0",
			expected: false,
		},
		{
			name:     "older major version",
			current:  "0.1.0",
			stored:   "1.0.0",
			expected: false,
		},
		{
			name:     "older minor version",
			current:  "0.1.0",
			stored:   "0.2.0",
			expected: false,
		},
		{
			name:     "older patch version",
			current:  "0.1.0",
			stored:   "0.1.1",
			expected: false,
		},
		{
			name:     "empty stored version",
			current:  "0.1.0",
			stored:   "",
			expected: true,
		},
		{
			name:     "different length versions - current longer",
			current:  "1.0.0.1",
			stored:   "1.0.0",
			expected: true,
		},
		{
			name:     "different length versions - stored longer",
			current:  "1.0.0",
			stored:   "1.0.0.1",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isNewerVersion(tt.current, tt.stored)
			assert.Equal(t, tt.expected, result, "isNewerVersion(%s, %s)", tt.current, tt.stored)
		})
	}
}

func TestBackupManager_SetupAndGetConfig(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "appmon-backup-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create a fake binary
	fakeBinary := filepath.Join(tmpDir, "appmon")
	err = os.WriteFile(fakeBinary, []byte("fake binary content"), 0755)
	require.NoError(t, err)

	// Create backup manager with custom home
	bm := &BackupManager{
		homeDir:    tmpDir,
		configPath: filepath.Join(tmpDir, ".config", ".helper.json"),
	}

	// Test SetupBackups
	err = bm.SetupBackups(fakeBinary, "1.0.0", "2024-01-01")
	require.NoError(t, err)

	// Test GetConfig
	config, err := bm.GetConfig()
	require.NoError(t, err)

	assert.Equal(t, fakeBinary, config.MainBinaryPath)
	assert.Equal(t, "1.0.0", config.Version)
	assert.Equal(t, "2024-01-01", config.BuildTime)
	assert.NotEmpty(t, config.SHA256)
	assert.NotEmpty(t, config.BackupPaths)
}

func TestBackupManager_VerifyAndRestore_BinaryMissing(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "appmon-backup-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create initial binary and setup backups
	binaryPath := filepath.Join(tmpDir, "appmon")
	err = os.WriteFile(binaryPath, []byte("original content"), 0755)
	require.NoError(t, err)

	bm := &BackupManager{
		homeDir:    tmpDir,
		configPath: filepath.Join(tmpDir, ".config", ".helper.json"),
	}

	err = bm.SetupBackups(binaryPath, "1.0.0", "")
	require.NoError(t, err)

	// Delete the binary
	err = os.Remove(binaryPath)
	require.NoError(t, err)

	// Verify and restore
	restored, err := bm.VerifyAndRestore()
	require.NoError(t, err)
	assert.True(t, restored, "Binary should be restored")

	// Check binary was restored
	_, err = os.Stat(binaryPath)
	assert.NoError(t, err, "Binary should exist after restore")

	// Verify content matches
	content, err := os.ReadFile(binaryPath)
	require.NoError(t, err)
	assert.Equal(t, "original content", string(content))
}

func TestBackupManager_VerifyAndRestore_SHAMatches(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "appmon-backup-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	binaryPath := filepath.Join(tmpDir, "appmon")
	err = os.WriteFile(binaryPath, []byte("binary content"), 0755)
	require.NoError(t, err)

	bm := &BackupManager{
		homeDir:    tmpDir,
		configPath: filepath.Join(tmpDir, ".config", ".helper.json"),
	}

	err = bm.SetupBackups(binaryPath, "1.0.0", "")
	require.NoError(t, err)

	// Don't modify - SHA should match
	restored, err := bm.VerifyAndRestore()
	require.NoError(t, err)
	assert.False(t, restored, "Binary should not be restored when SHA matches")
}

func TestComputeSHA256(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "appmon-sha-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name    string
		content string
	}{
		{name: "empty file", content: ""},
		{name: "small content", content: "hello"},
		{name: "larger content", content: "this is a longer piece of content for testing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := filepath.Join(tmpDir, "test-"+tt.name)
			err := os.WriteFile(filePath, []byte(tt.content), 0644)
			require.NoError(t, err)

			hash1, err := computeSHA256(filePath)
			require.NoError(t, err)

			// Same content should produce same hash
			hash2, err := computeSHA256(filePath)
			require.NoError(t, err)
			assert.Equal(t, hash1, hash2)

			// Hash should be 64 hex characters (256 bits = 32 bytes = 64 hex)
			assert.Len(t, hash1, 64)
		})
	}
}

func TestComputeSHA256_FileNotFound(t *testing.T) {
	_, err := computeSHA256("/nonexistent/path/to/file")
	assert.Error(t, err)
}
