package sig

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// pemPath returns the source PEM next to this test file. The PEM is kept
// in-tree as the source of truth for the generator (and CI cross-checks).
func pemPath(t *testing.T) string {
	t.Helper()
	_, f, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(f), "focusd_ed25519_public.pem")
}

// TestLoadPublicKeyPEMRoundtrip proves the XOR-decoded bytes are exactly
// the on-disk PEM (i.e. the generator + decoder are a true inverse pair).
func TestLoadPublicKeyPEMRoundtrip(t *testing.T) {
	want, err := os.ReadFile(pemPath(t))
	if err != nil {
		t.Fatalf("read source PEM: %v", err)
	}
	got := loadPublicKeyPEM()
	if !bytes.Equal(got, want) {
		t.Fatalf("decoded PEM differs from source\n got:  %q\n want: %q", got, want)
	}
}

// TestMaskedPubkeyHasNoPEMLiterals: the whole point of masking is that
// these strings disappear from the binary. If they ever reappear in the
// masked blob the friction-layer is silently broken.
func TestMaskedPubkeyHasNoPEMLiterals(t *testing.T) {
	for _, needle := range [][]byte{
		[]byte("BEGIN PUBLIC KEY"),
		[]byte("END PUBLIC KEY"),
		[]byte("-----BEGIN"),
		[]byte("-----END"),
	} {
		if bytes.Contains(maskedPubkey, needle) {
			t.Errorf("maskedPubkey leaks literal %q", needle)
		}
	}

	// Heuristic: no 16-byte run of printable ASCII. The PEM body is
	// base64 (all printable); a working XOR mask should scramble that
	// into bytes with the high bit set roughly half the time. If the
	// mask is ever effectively zero/identity, this run will appear.
	const runLen = 16
	streak := 0
	for _, b := range maskedPubkey {
		if b >= 0x20 && b < 0x7f {
			streak++
			if streak >= runLen {
				t.Fatalf("maskedPubkey has a %d-byte printable-ASCII run; mask is ineffective", runLen)
			}
		} else {
			streak = 0
		}
	}
}

// TestVerifyStillWorksAfterMaskRefactor signs a payload with the real
// offline private key and verifies it with the XOR-decoded public key.
// Skipped where the private key isn't present (same convention as the
// existing offline cross-check test).
func TestVerifyStillWorksAfterMaskRefactor(t *testing.T) {
	privPath := privateKeyPath(t)
	if privPath == "" {
		t.Skip("no offline private key (env or ~/.creds); skipping roundtrip")
	}
	dir := t.TempDir()
	msgPath := filepath.Join(dir, "msg.bin")
	sigPath := filepath.Join(dir, "msg.sig")
	if err := os.WriteFile(msgPath, []byte("focusd mask refactor roundtrip"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("openssl", "pkeyutl", "-sign", "-inkey", privPath,
		"-rawin", "-in", msgPath, "-out", sigPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("openssl sign unavailable: %v\n%s", err, out)
	}
	sigBytes, _ := os.ReadFile(sigPath)
	msgBytes, _ := os.ReadFile(msgPath)
	ok, err := Verify(msgBytes, sigBytes)
	if err != nil || !ok {
		t.Fatalf("XOR-decoded public key does NOT verify against offline private key: ok=%v err=%v", ok, err)
	}
	// Sanity: the decoded key is a valid Ed25519 public key of the
	// correct length.
	pub, err := PublicKey()
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Fatalf("decoded key size %d, want %d", len(pub), ed25519.PublicKeySize)
	}
}

// privateKeyPath returns a filesystem path to a usable Ed25519 PEM, or ""
// if none is available. If the env var is set with PEM contents (not a
// path), the contents are written to a temp file so openssl can read it.
func privateKeyPath(t *testing.T) string {
	t.Helper()
	if env := os.Getenv("FOCUSD_ED25519_PRIVATE_KEY"); env != "" {
		// env may be either a path or raw PEM contents.
		if _, err := os.Stat(env); err == nil {
			return env
		}
		f, err := os.CreateTemp(t.TempDir(), "focusd-priv-*.pem")
		if err != nil {
			t.Fatalf("temp key: %v", err)
		}
		if _, err := f.WriteString(env); err != nil {
			t.Fatalf("write temp key: %v", err)
		}
		f.Close()
		return f.Name()
	}
	p := os.ExpandEnv("$HOME/.creds/focusd_ed25519_private.pem")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}
