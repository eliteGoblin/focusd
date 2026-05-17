package sig

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

// privateKeyFromPEM parses an Ed25519 PKCS#8 PEM private key (as written
// by `openssl genpkey -algorithm ed25519`).
func privateKeyFromPEM(pemBytes []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("sig: private key is not valid PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("sig: parse PKCS8: %w", err)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("sig: not an Ed25519 private key")
	}
	return priv, nil
}

// SignFile reads program at inPath, appends a 64-byte Ed25519 signature
// trailer (signed with the PEM private key), and writes the result to
// outPath with mode 0o755. This is the release/build step — the only
// place the private key is used. mode preserves executability.
func SignFile(inPath, outPath string, privPEM []byte) error {
	priv, err := privateKeyFromPEM(privPEM)
	if err != nil {
		return err
	}
	program, err := os.ReadFile(inPath)
	if err != nil {
		return fmt.Errorf("sig: read %s: %w", inPath, err)
	}
	signature := ed25519.Sign(priv, program)
	if len(signature) != SigLen {
		return fmt.Errorf("sig: unexpected signature length %d", len(signature))
	}
	out := append(program, signature...)
	if err := os.WriteFile(outPath, out, 0o755); err != nil {
		return fmt.Errorf("sig: write %s: %w", outPath, err)
	}
	return nil
}
