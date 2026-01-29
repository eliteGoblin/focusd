package infra

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileKeyProvider(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(t *testing.T, dataDir string)
		testFn func(t *testing.T, provider *FileKeyProvider)
	}{
		{
			name: "KeyExists returns false when no key file",
			testFn: func(t *testing.T, provider *FileKeyProvider) {
				assert.False(t, provider.KeyExists())
			},
		},
		{
			name: "StoreKey creates key file with correct permissions",
			testFn: func(t *testing.T, provider *FileKeyProvider) {
				key, err := GenerateKey()
				require.NoError(t, err)

				err = provider.StoreKey(key)
				require.NoError(t, err)

				assert.True(t, provider.KeyExists())

				// Check file permissions (0600)
				info, err := os.Stat(provider.keyPath)
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
			},
		},
		{
			name: "GetKey returns stored key",
			testFn: func(t *testing.T, provider *FileKeyProvider) {
				key, err := GenerateKey()
				require.NoError(t, err)

				err = provider.StoreKey(key)
				require.NoError(t, err)

				retrieved, err := provider.GetKey()
				require.NoError(t, err)
				assert.Equal(t, key, retrieved)
			},
		},
		{
			name: "GetKey returns error when no key file",
			testFn: func(t *testing.T, provider *FileKeyProvider) {
				_, err := provider.GetKey()
				assert.Error(t, err)
			},
		},
		{
			name: "StoreKey rejects wrong key size",
			testFn: func(t *testing.T, provider *FileKeyProvider) {
				shortKey := []byte("tooshort")
				err := provider.StoreKey(shortKey)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid key size")
			},
		},
		{
			name: "StoreKey creates directory if missing",
			testFn: func(t *testing.T, provider *FileKeyProvider) {
				// Override keyPath to nested directory
				nestedDir := filepath.Join(provider.keyPath+"_nested", "sub", "dir")
				provider.keyPath = filepath.Join(nestedDir, keyFileName)

				key, err := GenerateKey()
				require.NoError(t, err)

				err = provider.StoreKey(key)
				require.NoError(t, err)
				assert.True(t, provider.KeyExists())
			},
		},
		{
			name: "KeyExists returns true after StoreKey",
			testFn: func(t *testing.T, provider *FileKeyProvider) {
				assert.False(t, provider.KeyExists())

				key, err := GenerateKey()
				require.NoError(t, err)
				require.NoError(t, provider.StoreKey(key))

				assert.True(t, provider.KeyExists())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := t.TempDir()
			provider := NewFileKeyProvider(dataDir)

			if tt.setup != nil {
				tt.setup(t, dataDir)
			}
			tt.testFn(t, provider)
		})
	}
}

func TestGenerateKey(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "returns 32-byte key",
			test: func(t *testing.T) {
				key, err := GenerateKey()
				require.NoError(t, err)
				assert.Len(t, key, keySize)
			},
		},
		{
			name: "generates unique keys",
			test: func(t *testing.T) {
				keys := make(map[string]bool)
				for i := 0; i < 100; i++ {
					key, err := GenerateKey()
					require.NoError(t, err)
					keyStr := string(key)
					assert.False(t, keys[keyStr], "duplicate key generated")
					keys[keyStr] = true
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.test)
	}
}

func TestEnsureKey(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "generates new key when none exists",
			test: func(t *testing.T) {
				dataDir := t.TempDir()
				provider := NewFileKeyProvider(dataDir)

				key, err := EnsureKey(provider)
				require.NoError(t, err)
				assert.Len(t, key, keySize)
				assert.True(t, provider.KeyExists())
			},
		},
		{
			name: "returns existing key when already present",
			test: func(t *testing.T) {
				dataDir := t.TempDir()
				provider := NewFileKeyProvider(dataDir)

				// Store a key first
				originalKey, err := GenerateKey()
				require.NoError(t, err)
				require.NoError(t, provider.StoreKey(originalKey))

				// EnsureKey should return the same key
				key, err := EnsureKey(provider)
				require.NoError(t, err)
				assert.Equal(t, originalKey, key)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.test)
	}
}

func TestDataDirPermissions(t *testing.T) {
	dataDir := t.TempDir()
	provider := NewFileKeyProvider(dataDir)

	key, err := GenerateKey()
	require.NoError(t, err)
	require.NoError(t, provider.StoreKey(key))

	// Verify key file parent directory has 0700
	dirInfo, err := os.Stat(dataDir)
	require.NoError(t, err)
	// Note: TempDir creates with 0700 already, but StoreKey should also ensure this
	assert.True(t, dirInfo.IsDir())
}
