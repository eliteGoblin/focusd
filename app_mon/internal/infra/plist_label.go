package infra

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

const (
	// SecretKeyPlistLabel is the secret store key for the randomized plist label.
	SecretKeyPlistLabel = "plist_label"

	// plistLabelPrefix makes the label look like a macOS system service.
	plistLabelPrefix = "com.apple.xpc.launchd.helper"
)

// EnsurePlistLabel generates a random plist label on first install,
// or retrieves the existing one from the secret store.
// The label is also set as the active launchd label.
func EnsurePlistLabel(store domain.SecretStore) (string, error) {
	label, err := store.GetSecret(SecretKeyPlistLabel)
	if err == nil && label != "" {
		SetLaunchdLabel(label)
		return label, nil
	}

	// Generate a new randomized label
	label, err = generatePlistLabel()
	if err != nil {
		return "", fmt.Errorf("failed to generate plist label: %w", err)
	}

	if err := store.SetSecret(SecretKeyPlistLabel, label); err != nil {
		return "", fmt.Errorf("failed to store plist label: %w", err)
	}

	SetLaunchdLabel(label)
	return label, nil
}

// generatePlistLabel creates a random label like "com.apple.xpc.launchd.helper.a8f3b2c1".
func generatePlistLabel() (string, error) {
	b := make([]byte, 4) // 8 hex chars
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%s", plistLabelPrefix, hex.EncodeToString(b)), nil
}
