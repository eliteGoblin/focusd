package sig

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// generic verification logic, ephemeral keys (no dependency on the real key)
func TestVerifyWith(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	msg := []byte("genuine focusd program bytes")
	good := ed25519.Sign(priv, msg)

	if !verifyWith(pub, msg, good) {
		t.Fatal("valid signature must verify")
	}
	if verifyWith(pub, []byte("tampered"), good) {
		t.Fatal("modified message must NOT verify")
	}
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if verifyWith(otherPub, msg, good) {
		t.Fatal("wrong key must NOT verify (no forgery)")
	}
}

func TestSplitTrailer(t *testing.T) {
	if _, _, err := SplitTrailer(make([]byte, SigLen)); err == nil {
		t.Fatal("file <= SigLen must error")
	}
	file := append([]byte("PROGRAM"), make([]byte, SigLen)...)
	prog, sig, err := SplitTrailer(file)
	if err != nil {
		t.Fatalf("SplitTrailer: %v", err)
	}
	if string(prog) != "PROGRAM" || len(sig) != SigLen {
		t.Fatalf("bad split: prog=%q sig=%d", prog, len(sig))
	}
}

func TestEmbeddedPublicKeyParses(t *testing.T) {
	pub, err := PublicKey()
	if err != nil {
		t.Fatalf("embedded public key must parse: %v", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Fatalf("embedded key size %d, want %d", len(pub), ed25519.PublicKeySize)
	}
}

// Proves the embedded public key matches the real offline private key.
// Skipped where the private key isn't present (e.g. plain CI runners).
func TestEmbeddedKeyMatchesOfflinePrivate(t *testing.T) {
	priv := os.ExpandEnv("$HOME/.creds/focusd_ed25519_private.pem")
	if _, err := os.Stat(priv); err != nil {
		t.Skip("offline private key not present; skipping cross-check")
	}
	dir := t.TempDir()
	msgPath := filepath.Join(dir, "msg.bin")
	sigPath := filepath.Join(dir, "msg.sig")
	if err := os.WriteFile(msgPath, []byte("focusd release bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Sign with the real offline private key via openssl (Ed25519 raw).
	cmd := exec.Command("openssl", "pkeyutl", "-sign", "-inkey", priv,
		"-rawin", "-in", msgPath, "-out", sigPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("openssl sign unavailable: %v\n%s", err, out)
	}
	sigBytes, _ := os.ReadFile(sigPath)
	msgBytes, _ := os.ReadFile(msgPath)
	ok, err := Verify(msgBytes, sigBytes)
	if err != nil || !ok {
		t.Fatalf("embedded public key does NOT match offline private key: ok=%v err=%v", ok, err)
	}
}

func TestVerifyFileRejectsUnsigned(t *testing.T) {
	f := filepath.Join(t.TempDir(), "unsigned")
	os.WriteFile(f, append([]byte("not a signed binary"), make([]byte, SigLen)...), 0o644)
	ok, err := VerifyFile(f)
	if err != nil {
		t.Fatalf("VerifyFile err: %v", err)
	}
	if ok {
		t.Fatal("random trailer must NOT verify as genuine")
	}
}
