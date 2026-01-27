package infra

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// macOS system-like process name patterns.
// These are designed to blend in with legitimate system processes.
var prefixes = []string{
	"com.apple.cfprefsd",
	"com.apple.metadata",
	"com.apple.security",
	"com.apple.xpc",
	"com.apple.coreservices",
	"com.apple.finder",
	"com.apple.launchd",
	"com.apple.diskarbitrationd",
}

var suffixes = []string{
	"xpc",
	"helper",
	"agent",
	"service",
	"worker",
	"monitor",
}

// ObfuscatorImpl implements domain.Obfuscator.
type ObfuscatorImpl struct{}

// NewObfuscator creates a new process name obfuscator.
func NewObfuscator() domain.Obfuscator {
	return &ObfuscatorImpl{}
}

// GenerateName creates a random system-looking process name.
// Examples:
//   - com.apple.cfprefsd.xpc.a1b2c3
//   - com.apple.metadata.helper.d4e5f6
//   - com.apple.security.worker.789abc
func (o *ObfuscatorImpl) GenerateName() string {
	prefix := prefixes[randomInt(len(prefixes))]
	suffix := suffixes[randomInt(len(suffixes))]
	randomID := generateRandomHex(6)

	return fmt.Sprintf("%s.%s.%s", prefix, suffix, randomID)
}

// randomInt returns a cryptographically random int in [0, max).
func randomInt(max int) int {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return int(n.Int64())
}

// generateRandomHex generates a random hex string of specified length.
func generateRandomHex(length int) string {
	bytes := make([]byte, length/2+1)
	if _, err := rand.Read(bytes); err != nil {
		return "000000"
	}
	return hex.EncodeToString(bytes)[:length]
}

// Ensure ObfuscatorImpl implements domain.Obfuscator.
var _ domain.Obfuscator = (*ObfuscatorImpl)(nil)
