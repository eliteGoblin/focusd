package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rosterLabels() []string {
	return []string{
		"com.apple.metadata.helper.7f3a2c11ab",
		"com.google.keystone.daemon.8c1f4e9d22",
		"org.mozilla.updater.agent.0a1b2c3d4e",
	}
}

// TestRosterRoundTrip asserts acceptance #3 recovery: WriteRoster then
// ReadRoster returns the exact same three labels (cold-start recovery).
func TestRosterRoundTrip(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	in := rosterLabels()
	if err := WriteRoster(s.RosterPath(), in); err != nil {
		t.Fatalf("WriteRoster: %v", err)
	}
	out, err := ReadRoster(s.RosterPath())
	if err != nil {
		t.Fatalf("ReadRoster: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("len = %d, want %d (%v)", len(out), len(in), out)
	}
	for i := range in {
		if out[i] != in[i] {
			t.Errorf("label[%d] = %q, want %q", i, out[i], in[i])
		}
	}
}

// TestRosterFileIsMasked asserts acceptance #3 masking: a raw `cat`-style
// read of the roster file shows masked bytes, NOT the plaintext labels.
func TestRosterFileIsMasked(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	in := rosterLabels()
	if err := WriteRoster(s.RosterPath(), in); err != nil {
		t.Fatalf("WriteRoster: %v", err)
	}
	raw, err := os.ReadFile(s.RosterPath())
	if err != nil {
		t.Fatalf("read raw: %v", err)
	}
	rawStr := string(raw)
	for _, label := range in {
		if strings.Contains(rawStr, label) {
			t.Fatalf("roster file leaks plaintext label %q", label)
		}
	}
	// The vendor stems must not appear in the clear either.
	for _, stem := range []string{"com.apple", "com.google", "org.mozilla"} {
		if strings.Contains(rawStr, stem) {
			t.Fatalf("roster file leaks plaintext stem %q", stem)
		}
	}
}

// TestRosterFileMode0600 asserts the roster is written 0600 (owner-only),
// not the 0644 the generic atomicWrite uses — the roster is sensitive.
func TestRosterFileMode0600(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if err := WriteRoster(s.RosterPath(), rosterLabels()); err != nil {
		t.Fatalf("WriteRoster: %v", err)
	}
	fi, err := os.Stat(s.RosterPath())
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("roster mode = %o, want 0600", perm)
	}
}

// TestReadRosterMissing asserts acceptance #4: a missing roster file
// errors cleanly so the caller can rewrite it from the in-memory roster.
func TestReadRosterMissing(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if _, err := ReadRoster(s.RosterPath()); err == nil {
		t.Fatal("ReadRoster on missing file must error")
	}
}

// TestReadRosterCorrupt asserts acceptance #4: a tampered/corrupt roster
// file errors cleanly (it does not return garbage labels), so a running
// worker rewrites it from memory.
func TestReadRosterCorrupt(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	// Empty file (no version byte) → error.
	if err := os.WriteFile(s.RosterPath(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRoster(s.RosterPath()); err == nil {
		t.Fatal("ReadRoster on empty file must error")
	}
	// Wrong version byte → error.
	if err := os.WriteFile(s.RosterPath(), []byte{0xff, 0x01, 0x02}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRoster(s.RosterPath()); err == nil {
		t.Fatal("ReadRoster on wrong-version payload must error")
	}
}

// TestRosterRewriteAfterTamper asserts the self-heal contract: after the
// file is corrupted, WriteRoster from memory restores a readable roster.
func TestRosterRewriteAfterTamper(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	in := rosterLabels()
	if err := WriteRoster(s.RosterPath(), in); err != nil {
		t.Fatal(err)
	}
	// Tamper.
	if err := os.WriteFile(s.RosterPath(), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRoster(s.RosterPath()); err == nil {
		t.Fatal("expected corrupt read to error before rewrite")
	}
	// Rewrite from memory heals it.
	if err := WriteRoster(s.RosterPath(), in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadRoster(s.RosterPath())
	if err != nil {
		t.Fatalf("ReadRoster after rewrite: %v", err)
	}
	if len(out) != 3 || out[0] != in[0] {
		t.Fatalf("rewrite did not heal roster: %v", out)
	}
}

// TestRosterAtomicTempCleanup asserts WriteRoster leaves no .tmp file.
func TestRosterAtomicTempCleanup(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if err := WriteRoster(s.RosterPath(), rosterLabels()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.RosterPath() + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
	_ = filepath.Dir(s.RosterPath())
}
