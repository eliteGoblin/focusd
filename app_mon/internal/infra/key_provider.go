package infra

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

const (
	keyFileName = ".key"
	keySize     = 32 // 256-bit AES key
)

// FileKeyProvider implements domain.KeyProvider using a local file.
// Phase 1: key stored in hidden file with 0600 permissions.
// Phase 2: can be replaced by server-generated key provider.
type FileKeyProvider struct {
	keyPath string
}

// NewFileKeyProvider creates a FileKeyProvider for the given data directory.
func NewFileKeyProvider(dataDir string) *FileKeyProvider {
	return &FileKeyProvider{
		keyPath: filepath.Join(dataDir, keyFileName),
	}
}

// GetKey reads the encryption key from the key file.
func (p *FileKeyProvider) GetKey() ([]byte, error) {
	encoded, err := os.ReadFile(p.keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read key file: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		return nil, fmt.Errorf("failed to decode key: %w", err)
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("invalid key size: got %d, want %d", len(key), keySize)
	}
	return key, nil
}

// StoreKey writes the encryption key to the key file with restricted permissions.
func (p *FileKeyProvider) StoreKey(key []byte) error {
	if len(key) != keySize {
		return fmt.Errorf("invalid key size: got %d, want %d", len(key), keySize)
	}
	// Ensure directory exists
	dir := filepath.Dir(p.keyPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create key directory: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(p.keyPath, []byte(encoded), 0600); err != nil {
		return fmt.Errorf("failed to write key file: %w", err)
	}
	return nil
}

// KeyExists checks if the key file exists.
func (p *FileKeyProvider) KeyExists() bool {
	_, err := os.Stat(p.keyPath)
	return err == nil
}

// GenerateKey creates a new random 256-bit encryption key.
func GenerateKey() ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate random key: %w", err)
	}
	return key, nil
}

// EnsureKey generates and stores a key if one doesn't exist.
// Returns the key (existing or newly generated).
func EnsureKey(provider domain.KeyProvider) ([]byte, error) {
	if provider.KeyExists() {
		return provider.GetKey()
	}
	key, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	if err := provider.StoreKey(key); err != nil {
		return nil, err
	}
	return key, nil
}

// Ensure FileKeyProvider implements domain.KeyProvider.
var _ domain.KeyProvider = (*FileKeyProvider)(nil)
