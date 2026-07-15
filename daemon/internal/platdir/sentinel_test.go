package platdir

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/relocate"
)

// TestMagicsAreDistinct: the platform-workdir and daemon-home magics differ, so a
// dir marked as one is never recognised as the other.
func TestMagicsAreDistinct(t *testing.T) {
	pw, dh := pwdMagic(), dhMagic()
	if len(pw) != magicLen || len(dh) != magicLen {
		t.Fatalf("magic lengths: pwd=%d dh=%d, want %d", len(pw), len(dh), magicLen)
	}
	if string(pw) == string(dh) {
		t.Fatal("platform-workdir and daemon-home magics must differ")
	}
}

// TestSentinelRecognition: a dir marked as a platform-workdir is IsPlatformWorkdir
// and NOT IsDaemonHome, and vice-versa; an unmarked dir is neither.
func TestSentinelRecognition(t *testing.T) {
	root := t.TempDir()

	pwDir := filepath.Join(root, "PlatWorkStore")
	if err := os.MkdirAll(pwDir, 0o700); err != nil {
		t.Fatal(err)
	}
	MarkPlatformWorkdir(pwDir)
	if !IsPlatformWorkdir(pwDir) {
		t.Fatal("marked platform-workdir must be IsPlatformWorkdir")
	}
	if IsDaemonHome(pwDir) {
		t.Fatal("platform-workdir must NOT be IsDaemonHome (distinct magic)")
	}

	dhDir := filepath.Join(root, "VendorAgent")
	if err := os.MkdirAll(dhDir, 0o700); err != nil {
		t.Fatal(err)
	}
	MarkDaemonHome(dhDir)
	if !IsDaemonHome(dhDir) {
		t.Fatal("marked daemon-home must be IsDaemonHome")
	}
	if IsPlatformWorkdir(dhDir) {
		t.Fatal("daemon-home must NOT be IsPlatformWorkdir (distinct magic)")
	}

	plain := filepath.Join(root, "Google")
	if err := os.MkdirAll(plain, 0o700); err != nil {
		t.Fatal(err)
	}
	if IsPlatformWorkdir(plain) || IsDaemonHome(plain) {
		t.Fatal("an unmarked real-app dir must be neither")
	}
}

// TestSentinelNeverMatchesRandom16ByteFile: a real file that is exactly magicLen
// bytes but random content must NOT be recognised (only content==magic matches).
func TestSentinelNeverMatchesRandom16ByteFile(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 200; i++ {
		b := make([]byte, magicLen)
		_, _ = rand.Read(b)
		if err := os.WriteFile(filepath.Join(dir, ".rand"), b, 0o644); err != nil {
			t.Fatal(err)
		}
		if IsPlatformWorkdir(dir) || IsDaemonHome(dir) {
			t.Fatalf("random 16-byte content must never match a magic: %x", b)
		}
	}
}

// TestLegacyPlatformWorkdirRecognition: the legacy two-signal marker (fixed
// sentinel basename AND state.db) IS recognised as a platform-workdir; either
// signal alone is NOT.
func TestLegacyPlatformWorkdirRecognition(t *testing.T) {
	root := t.TempDir()

	both := filepath.Join(root, "legacyBoth")
	if err := os.MkdirAll(both, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(both, legacyPwdSentinelBasename), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(both, legacyStateDBBasename), []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsPlatformWorkdir(both) {
		t.Fatal("legacy two-signal dir must be recognised")
	}

	sentOnly := filepath.Join(root, "sentOnly")
	if err := os.MkdirAll(sentOnly, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sentOnly, legacyPwdSentinelBasename), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if IsPlatformWorkdir(sentOnly) {
		t.Fatal("legacy sentinel WITHOUT state.db must NOT match")
	}

	dbOnly := filepath.Join(root, "dbOnly")
	if err := os.MkdirAll(dbOnly, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dbOnly, legacyStateDBBasename), []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsPlatformWorkdir(dbOnly) {
		t.Fatal("state.db WITHOUT the legacy sentinel must NOT match")
	}
}

// TestPointerMaskRoundTrip: with a salt present, the pointer file CONTENT is
// masked (not plaintext) yet Read returns the exact path; the basename is
// salt-derived (not the legacy literal).
func TestPointerMaskRoundTrip(t *testing.T) {
	root := t.TempDir()
	dh := filepath.Join(root, "VendorAgent")
	if err := os.MkdirAll(dh, 0o700); err != nil {
		t.Fatal(err)
	}
	// Seed a salt so pointer masking + salt-derived basename engage.
	if err := os.WriteFile(filepath.Join(dh, relocate.SaltBasename), []byte("deadbeefcafef00d"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "PlatWorkStore")
	if err := Write(dh, target); err != nil {
		t.Fatal(err)
	}
	if got := Read(dh); got != target {
		t.Fatalf("Read = %q, want %q", got, target)
	}
	// The on-disk bytes must NOT contain the plaintext path (masked).
	raw, err := os.ReadFile(PointerPath(dh))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == target+"\n" {
		t.Fatal("pointer content must be masked, not plaintext")
	}
	// The basename must be salt-derived, not the fixed legacy literal.
	if filepath.Base(PointerPath(dh)) == legacyPointerBasename {
		t.Fatal("pointer basename must be salt-derived when a salt is present")
	}
}

// TestPointerLegacyPlaintextAccepted: a pre-FEATURE-26 install wrote a PLAINTEXT
// pointer at the legacy basename before a salt existed. After the salt is seeded,
// Read must still recover the live platform-workdir (migration: don't orphan it).
func TestPointerLegacyPlaintextAccepted(t *testing.T) {
	root := t.TempDir()
	dh := filepath.Join(root, "VendorAgent")
	if err := os.MkdirAll(dh, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "PlatWorkStore")
	// Legacy: plaintext content at the legacy basename, NO salt yet.
	if err := os.WriteFile(filepath.Join(dh, legacyPointerBasename), []byte(target+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Read(dh); got != target {
		t.Fatalf("legacy plaintext pointer (no salt) Read = %q, want %q", got, target)
	}
	// Now the upgrade seeds the salt: Read must STILL find the live workdir via the
	// legacy-basename fallback (the new basename does not exist yet).
	if err := os.WriteFile(filepath.Join(dh, relocate.SaltBasename), []byte("00112233445566778899aabbccddeeff"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Read(dh); got != target {
		t.Fatalf("post-upgrade Read = %q, want %q (must adopt the legacy pointer)", got, target)
	}
}

// TestPlatformWorkdirCreateIsExclusiveAndMarked: Create makes a fresh, marked dir
// and never adopts a pre-existing directory of the same name (destructive-safety
// invariant).
func TestPlatformWorkdirCreateIsExclusiveAndMarked(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != root {
		t.Fatalf("created dir %q not under root %q", dir, root)
	}
	if !IsPlatformWorkdir(dir) {
		t.Fatal("Create must mark the platform-workdir")
	}
	// Second Create must land at a DIFFERENT path (never adopt the first).
	dir2, err := Create(root)
	if err != nil {
		t.Fatal(err)
	}
	if dir2 == dir {
		t.Fatal("Create must never adopt an existing dir")
	}
}
