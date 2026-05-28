// Package sig verifies that a binary is a genuine, unmodified focusd
// release: signed by the offline Ed25519 private key. The matching
// PUBLIC key is compiled in XOR-masked (see pubkey.go +
// pubkey_masked.go) so a trivial `strings | grep "BEGIN PUBLIC"`
// against the daemon binary doesn't locate it. The daemon can never
// sign.
//
// Regenerate the masked pubkey if the offline PEM rotates:
//
//	go generate ./daemon/internal/sig
//
//go:generate go run ./gen/mask/main.go
package sig

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"sync"
)

// SigLen is the fixed Ed25519 signature length appended as the binary's
// trailer (the last SigLen bytes of a signed release file).
const SigLen = ed25519.SignatureSize // 64

var errTooSmall = errors.New("sig: file smaller than signature trailer")

// cachedPubKey holds the parsed Ed25519 public key after first use; the
// underlying PEM bytes are XOR-decoded from maskedPubkey lazily so the
// decoded form is short-lived rather than a long-lived package variable.
var (
	cachedPubKey ed25519.PublicKey
	cachedErr    error
	cacheOnce    sync.Once
)

// PublicKey returns the Ed25519 public key, decoding + parsing the
// XOR-masked PEM on first call.
func PublicKey() (ed25519.PublicKey, error) {
	cacheOnce.Do(func() {
		cachedPubKey, cachedErr = parsePublicKey(loadPublicKeyPEM())
	})
	return cachedPubKey, cachedErr
}

func parsePublicKey(pemBytes []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("sig: embedded public key is not valid PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("sig: parse public key: %w", err)
	}
	ed, ok := pub.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("sig: embedded key is not Ed25519")
	}
	return ed, nil
}

// Verify reports whether sig is a valid signature of msg under the
// embedded public key. Pure; no I/O.
func Verify(msg, signature []byte) (bool, error) {
	pub, err := PublicKey()
	if err != nil {
		return false, err
	}
	if len(signature) != SigLen {
		return false, fmt.Errorf("sig: signature length %d, want %d", len(signature), SigLen)
	}
	return verifyWith(pub, msg, signature), nil
}

// verifyWith is the keyed verification step, isolated so the logic is
// unit-testable with ephemeral keys (Verify itself is pinned to the
// embedded public key).
func verifyWith(pub ed25519.PublicKey, msg, signature []byte) bool {
	return ed25519.Verify(pub, msg, signature)
}

// SplitTrailer splits a signed release file's bytes into the program
// image and its appended 64-byte signature trailer.
func SplitTrailer(file []byte) (program, signature []byte, err error) {
	if len(file) <= SigLen {
		return nil, nil, errTooSmall
	}
	n := len(file) - SigLen
	return file[:n], file[n:], nil
}

// VerifyFile reads a release file (program ++ 64-byte sig trailer) and
// verifies it against the embedded public key. This is how the daemon
// recognises a genuine focusd peer/binary on disk — it trusts the math,
// never the file's self-report.
func VerifyFile(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("sig: read %s: %w", path, err)
	}
	program, signature, err := SplitTrailer(data)
	if err != nil {
		return false, err
	}
	return Verify(program, signature)
}
