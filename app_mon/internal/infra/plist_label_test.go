package infra

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratePlistLabel(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "has correct prefix",
			test: func(t *testing.T) {
				label, err := generatePlistLabel()
				require.NoError(t, err)
				assert.True(t, strings.HasPrefix(label, plistLabelPrefix+"."))
			},
		},
		{
			name: "has 8 hex chars suffix",
			test: func(t *testing.T) {
				label, err := generatePlistLabel()
				require.NoError(t, err)
				parts := strings.Split(label, ".")
				suffix := parts[len(parts)-1]
				assert.Len(t, suffix, 8)
			},
		},
		{
			name: "generates unique labels",
			test: func(t *testing.T) {
				seen := make(map[string]bool)
				for i := 0; i < 100; i++ {
					label, err := generatePlistLabel()
					require.NoError(t, err)
					assert.False(t, seen[label], "duplicate label: %s", label)
					seen[label] = true
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.test)
	}
}

func TestEnsurePlistLabel(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "generates new label on first call",
			test: func(t *testing.T) {
				reg, _ := newTestRegistry(t)
				label, err := EnsurePlistLabel(reg)
				require.NoError(t, err)
				assert.True(t, strings.HasPrefix(label, plistLabelPrefix+"."))
				assert.Equal(t, label, GetLaunchdLabel())
			},
		},
		{
			name: "returns existing label on subsequent calls",
			test: func(t *testing.T) {
				reg, _ := newTestRegistry(t)

				label1, err := EnsurePlistLabel(reg)
				require.NoError(t, err)

				label2, err := EnsurePlistLabel(reg)
				require.NoError(t, err)

				assert.Equal(t, label1, label2)
			},
		},
		{
			name: "label persists across registry instances",
			test: func(t *testing.T) {
				dataDir := t.TempDir()
				key, err := GenerateKey()
				require.NoError(t, err)
				pm := newMockProcessManager()

				// First instance: generate label
				reg1, err := NewEncryptedRegistry(dataDir, key, pm)
				require.NoError(t, err)
				label1, err := EnsurePlistLabel(reg1)
				require.NoError(t, err)
				reg1.Close()

				// Second instance: should get same label
				reg2, err := NewEncryptedRegistry(dataDir, key, pm)
				require.NoError(t, err)
				defer reg2.Close()
				label2, err := EnsurePlistLabel(reg2)
				require.NoError(t, err)

				assert.Equal(t, label1, label2)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset label to default before each test
			SetLaunchdLabel(DefaultLaunchdLabel)
			tt.test(t)
		})
	}
}
