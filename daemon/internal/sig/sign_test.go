package sig

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func writeEphemeralPEM(t *testing.T) (string, ed25519.PublicKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	p := filepath.Join(t.TempDir(), "k.pem")
	if err := os.WriteFile(p, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, pub
}

func TestSignFileRoundtrip(t *testing.T) {
	pemPath, pub := writeEphemeralPEM(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "prog")
	out := filepath.Join(dir, "prog.signed")
	if err := os.WriteFile(in, []byte("fake program bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	privPEM, _ := os.ReadFile(pemPath)
	if err := SignFile(in, out, privPEM); err != nil {
		t.Fatalf("SignFile: %v", err)
	}
	data, _ := os.ReadFile(out)
	program, signature, err := SplitTrailer(data)
	if err != nil {
		t.Fatalf("SplitTrailer: %v", err)
	}
	if string(program) != "fake program bytes" {
		t.Fatalf("program corrupted: %q", program)
	}
	if !verifyWith(pub, program, signature) {
		t.Fatal("signed file must verify with its public key")
	}
	// Executable bit preserved.
	fi, _ := os.Stat(out)
	if fi.Mode()&0o100 == 0 {
		t.Fatal("signed binary must stay executable")
	}
}

func TestSignFileRejectsBadPEM(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "p")
	os.WriteFile(in, []byte("x"), 0o755)
	if err := SignFile(in, filepath.Join(dir, "o"), []byte("not pem")); err == nil {
		t.Fatal("bad PEM must error")
	}
}

// End-to-end with the REAL offline key: sign with ~/.creds private key,
// verify with the embedded public key. Skipped where the key is absent.
func TestSignWithOfflineKeyVerifiesWithEmbedded(t *testing.T) {
	priv := os.ExpandEnv("$HOME/.creds/focusd_ed25519_private.pem")
	pemBytes, err := os.ReadFile(priv)
	if err != nil {
		t.Skip("offline private key not present")
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "prog")
	out := filepath.Join(dir, "prog.signed")
	os.WriteFile(in, []byte("focusd release program"), 0o755)
	if err := SignFile(in, out, pemBytes); err != nil {
		t.Fatalf("SignFile: %v", err)
	}
	ok, err := VerifyFile(out)
	if err != nil || !ok {
		t.Fatalf("offline-signed file must verify with embedded key: ok=%v err=%v", ok, err)
	}
}
