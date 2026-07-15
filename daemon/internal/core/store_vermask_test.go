package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// saltedStore returns a Store whose daemon-home already holds a salt, so the
// FEATURE 26 version masking + keyed bad-marker names engage.
func saltedStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, InstallSaltFile), []byte("cafef00ddeadbeefcafef00ddeadbeef"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Store{Dir: dir}
}

// TestVersionContentMaskedButReadable: with a salt, version.json / good / bad
// content is masked on disk (no plaintext version) yet reads back exactly.
func TestVersionContentMaskedButReadable(t *testing.T) {
	s := saltedStore(t)
	const v = "v0.16.7"

	if err := s.WriteDesired(v); err != nil {
		t.Fatal(err)
	}
	if got := s.Desired(); got != v {
		t.Fatalf("Desired = %q, want %q", got, v)
	}
	if err := s.WriteGood(v); err != nil {
		t.Fatal(err)
	}
	if got := s.Good(); got != v {
		t.Fatalf("Good = %q, want %q", got, v)
	}
	if err := s.MarkBad(v); err != nil {
		t.Fatal(err)
	}
	if !s.BadSet()[v] {
		t.Fatalf("BadSet missing %q", v)
	}

	// The plaintext version must NOT appear in any on-disk version-state file.
	for _, p := range []string{filepath.Join(s.Dir, VersionFile), filepath.Join(s.Dir, "good")} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), v) {
			t.Fatalf("%s leaks plaintext version %q: %q", filepath.Base(p), v, b)
		}
	}
	// The bad marker filename must be a keyed digest, not the version.
	entries, _ := os.ReadDir(filepath.Join(s.Dir, "bad"))
	for _, e := range entries {
		if strings.Contains(e.Name(), v) {
			t.Fatalf("bad marker FILENAME leaks version %q: %q", v, e.Name())
		}
		b, _ := os.ReadFile(filepath.Join(s.Dir, "bad", e.Name()))
		if strings.Contains(string(b), v) {
			t.Fatalf("bad marker CONTENT leaks plaintext version %q: %q", v, b)
		}
	}
}

// TestVersionLegacyPlaintextAccepted: a pre-FEATURE-26 install wrote PLAINTEXT
// version.json / good / bad markers. After the salt exists, the getters must still
// read them (accept-legacy → self-heals to masked on the next write).
func TestVersionLegacyPlaintextAccepted(t *testing.T) {
	s := saltedStore(t)
	const v = "v1.2.3"

	// Legacy plaintext files (as an older binary wrote them).
	if err := os.WriteFile(filepath.Join(s.Dir, VersionFile), []byte(`{"desired":"`+v+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "good"), []byte(v), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "bad", safe(v)), []byte(v), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := s.Desired(); got != v {
		t.Fatalf("legacy Desired = %q, want %q", got, v)
	}
	if got := s.Good(); got != v {
		t.Fatalf("legacy Good = %q, want %q", got, v)
	}
	if !s.BadSet()[v] {
		t.Fatalf("legacy BadSet missing %q", v)
	}
}

// TestClearBadRemovesBothNames: ClearBad removes both the keyed-digest marker and
// a legacy path-sanitised marker (so a pre-upgrade bad verdict is cleared).
func TestClearBadRemovesBothNames(t *testing.T) {
	s := saltedStore(t)
	const v = "v9.9.9"
	if err := os.MkdirAll(filepath.Join(s.Dir, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A legacy marker at the sanitised name + a new keyed-digest marker.
	if err := os.WriteFile(filepath.Join(s.Dir, "bad", safe(v)), []byte(v), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkBad(v); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearBad(v); err != nil {
		t.Fatal(err)
	}
	if s.BadSet()[v] {
		t.Fatalf("ClearBad must remove every marker for %q", v)
	}
	entries, _ := os.ReadDir(filepath.Join(s.Dir, "bad"))
	if len(entries) != 0 {
		t.Fatalf("bad dir must be empty after ClearBad, has %d entries", len(entries))
	}
}

// TestNoSaltKeepsLegacyLayout: with no salt, content is plaintext and the bad
// filename is the legacy sanitised name — the deterministic test/dev layout.
func TestNoSaltKeepsLegacyLayout(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	const v = "v0.1.0"
	if err := s.WriteDesired(v); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(s.Dir, VersionFile))
	if !strings.Contains(string(b), v) {
		t.Fatalf("no-salt version.json must be plaintext, got %q", b)
	}
	if err := s.MarkBad(v); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "bad", safe(v))); err != nil {
		t.Fatalf("no-salt bad marker must use the legacy sanitised name: %v", err)
	}
}
