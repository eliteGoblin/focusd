package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRoundtrips(t *testing.T) {
	s := &Store{Dir: t.TempDir()}

	if s.HaveConfig() || s.Desired() != "" || s.Good() != "" {
		t.Fatal("fresh store should be empty")
	}
	if err := s.WriteDesired("v1"); err != nil {
		t.Fatal(err)
	}
	if !s.HaveConfig() || s.Desired() != "v1" {
		t.Fatalf("desired roundtrip failed: %q", s.Desired())
	}
	if err := s.WriteGood("v1"); err != nil {
		t.Fatal(err)
	}
	if s.Good() != "v1" {
		t.Fatalf("good roundtrip failed: %q", s.Good())
	}
	if s.BadSet()["v2"] {
		t.Fatal("v2 should not be bad yet")
	}
	if err := s.MarkBad("v2"); err != nil {
		t.Fatal(err)
	}
	if !s.BadSet()["v2"] {
		t.Fatal("v2 should be bad after MarkBad")
	}
}

func TestStoreBinPath(t *testing.T) {
	s := &Store{Dir: "/wd"}
	if got := s.BinPath("v3"); got != filepath.Join("/wd", "bin", "v3", "platform") {
		t.Fatalf("BinPath = %q", got)
	}
	if s.HaveBin("v3") {
		t.Fatal("no bin should exist")
	}
}

func TestStoreSafeVersionStaysUnderBadDir(t *testing.T) {
	bad := "/store/bad"
	for _, v := range []string{"../../etc/passwd", "a/b", "x..y", "v 1.0", "ok"} {
		joined := filepath.Clean(filepath.Join(bad, safe(v)))
		if !strings.HasPrefix(joined+string(filepath.Separator), bad+string(filepath.Separator)) &&
			joined != bad {
			t.Fatalf("safe(%q) escapes bad dir: %s", v, joined)
		}
		if strings.Contains(safe(v), "..") || strings.ContainsRune(safe(v), filepath.Separator) {
			t.Fatalf("safe(%q)=%q still path-dangerous", v, safe(v))
		}
	}
}

func TestMarkBadRoundtripsSanitisedVersion(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	// A version containing a sanitised char must still be recognised
	// by its ORIGINAL string (the bug Copilot flagged).
	if err := s.MarkBad("v 1.0/beta"); err != nil {
		t.Fatal(err)
	}
	if !s.BadSet()["v 1.0/beta"] {
		t.Fatalf("bad lookup must match original version, got %v", s.BadSet())
	}
}

// WorkdirIntact is the GAP-1 wipe detector: it requires BOTH the workdir dir
// and the platform's state.db to be present. A missing dir (rm -rf) or a
// missing/absent state.db reads as broken.
func TestStoreWorkdirIntact(t *testing.T) {
	// Missing workdir dir → not intact.
	missing := &Store{Dir: filepath.Join(t.TempDir(), "gone")}
	if missing.WorkdirIntact() {
		t.Fatal("absent workdir must not read as intact")
	}

	// Dir present but no state.db → not intact.
	s := &Store{Dir: t.TempDir()}
	if s.WorkdirIntact() {
		t.Fatal("workdir with no state.db must not read as intact")
	}

	// Dir + state.db present → intact.
	if err := os.WriteFile(filepath.Join(s.Dir, PlatformStateDBName), []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !s.WorkdirIntact() {
		t.Fatal("workdir with state.db present must read as intact")
	}

	// state.db as a directory (not a file) → not intact.
	s2 := &Store{Dir: t.TempDir()}
	if err := os.Mkdir(filepath.Join(s2.Dir, PlatformStateDBName), 0o755); err != nil {
		t.Fatal(err)
	}
	if s2.WorkdirIntact() {
		t.Fatal("state.db that is a directory must not read as intact")
	}
}

func TestAtomicWriteCreatesDirs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "b", "f")
	if err := atomicWrite(p, []byte("x")); err != nil {
		t.Fatalf("atomicWrite: %v", err)
	}
	s := &Store{Dir: t.TempDir()}
	if err := s.WriteGood("v9"); err != nil || s.Good() != "v9" {
		t.Fatalf("write good through nested dirs failed: %v", err)
	}
}
