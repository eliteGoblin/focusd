package reconciler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- pure helpers ---

func TestRenderBlock(t *testing.T) {
	got := RenderBlock([]string{"a.com", "b.com"})
	want := BeginMarker + "\n0.0.0.0 a.com\n0.0.0.0 b.com\n" + EndMarker + "\n"
	if got != want {
		t.Fatalf("RenderBlock mismatch\nGOT:\n%s\nWANT:\n%s", got, want)
	}
}

func TestReplaceBlock_AppendsWhenAbsent(t *testing.T) {
	base := "127.0.0.1 localhost\n"
	desired := RenderBlock([]string{"x.com"})
	out := ReplaceBlock(base, desired)
	if !strings.Contains(out, "127.0.0.1 localhost") {
		t.Fatal("must preserve existing content")
	}
	if !strings.Contains(out, "0.0.0.0 x.com") {
		t.Fatal("must contain new block")
	}
}

func TestReplaceBlock_ReplacesExistingBlock(t *testing.T) {
	base := "127.0.0.1 localhost\n\n" + RenderBlock([]string{"old.com"}) + "\n192.168.1.1 tail\n"
	desired := RenderBlock([]string{"new.com"})
	out := ReplaceBlock(base, desired)
	if strings.Contains(out, "old.com") {
		t.Fatal("old block must be replaced")
	}
	if !strings.Contains(out, "0.0.0.0 new.com") {
		t.Fatal("new block must be present")
	}
	if !strings.Contains(out, "127.0.0.1 localhost") || !strings.Contains(out, "192.168.1.1 tail") {
		t.Fatal("surrounding content must survive replace")
	}
	// Idempotence: replacing again returns identical content.
	if again := ReplaceBlock(out, desired); again != out {
		t.Fatal("ReplaceBlock must be idempotent")
	}
}

func TestReplaceBlock_MalformedBeginWithoutEnd_SelfHealsInOneTick(t *testing.T) {
	// Truncated/malformed block (BEGIN but no END) → fresh block is
	// appended AND the orphaned BEGIN line is stripped in the SAME
	// write, so subsequent ticks see a well-formed file. (Regression
	// against an earlier two-tick self-heal that briefly left a stray
	// marker in the file.)
	base := "127.0.0.1 localhost\n" + BeginMarker + "\n0.0.0.0 truncated.com\n"
	desired := RenderBlock([]string{"x.com"})
	out := ReplaceBlock(base, desired)
	if !strings.Contains(out, "127.0.0.1 localhost") {
		t.Fatal("user content must survive")
	}
	if !strings.Contains(out, "0.0.0.0 x.com") {
		t.Fatal("fresh block must be appended")
	}
	// Exactly one BEGIN marker now (the one inside the fresh block).
	if got := strings.Count(out, BeginMarker); got != 1 {
		t.Fatalf("expected 1 BEGIN marker after heal, got %d:\n%s", got, out)
	}
	// truncated.com must be gone — it lived inside the orphaned block.
	if strings.Contains(out, "truncated.com") {
		t.Fatal("orphaned block contents must be cleaned")
	}
	// And the cleanup must be stable: a second pass is a noop.
	if again := ReplaceBlock(out, desired); again != out {
		t.Fatalf("self-heal must be stable; second pass differs:\n%s", again)
	}
}

// --- domain resolution ---

func TestResolveDomains_ExplicitWins(t *testing.T) {
	r := &Reconciler{Domains: []string{"b", "a", "a"}}
	got, err := r.resolveDomains()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("expected [a b], got %v", got)
	}
}

func TestResolveDomains_EmbeddedHasSteamAndPersonal(t *testing.T) {
	r := &Reconciler{}
	got, err := r.resolveDomains()
	if err != nil {
		t.Fatal(err)
	}
	// Spot checks — exact membership comes from the embedded txt files.
	mustContain := []string{"steampowered.com", "dota2.com", "youtube.com", "bilibili.com"}
	have := map[string]bool{}
	for _, d := range got {
		have[d] = true
	}
	for _, d := range mustContain {
		if !have[d] {
			t.Errorf("embedded blocklist missing %q", d)
		}
	}
	if len(got) < 50 {
		t.Errorf("embedded blocklist too small: %d entries (expected 60+)", len(got))
	}
	// Sorted + unique.
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("not strictly sorted/unique at %d: %q <= %q", i, got[i], got[i-1])
		}
	}
}

// --- end-to-end reconcile against a tempfile (no /etc/hosts touched) ---

func mkHosts(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "hosts")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func newR(path string, domains ...string) *Reconciler {
	return &Reconciler{
		HostsPath: path,
		Domains:   domains,
		GetEUID:   func() int { return 0 }, // pretend we're root
	}
}

func TestReconcile_AppliesOnFreshHosts(t *testing.T) {
	p := mkHosts(t, "127.0.0.1 localhost\n")
	o, err := newR(p, "blocked.com").Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if !o.Changed || o.Domains != 1 || o.Reason != "applied" {
		t.Fatalf("unexpected outcome: %+v", o)
	}
	content := read(t, p)
	if !strings.Contains(content, "127.0.0.1 localhost") {
		t.Fatal("existing content lost")
	}
	if !strings.Contains(content, "0.0.0.0 blocked.com") {
		t.Fatal("block not applied")
	}
}

func TestReconcile_Idempotent(t *testing.T) {
	p := mkHosts(t, "127.0.0.1 localhost\n")
	r := newR(p, "x.com", "y.com")
	if _, err := r.Reconcile(); err != nil {
		t.Fatal(err)
	}
	before := read(t, p)
	o, err := r.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if o.Changed || o.Reason != "noop" {
		t.Fatalf("second reconcile must be a noop, got %+v", o)
	}
	if read(t, p) != before {
		t.Fatal("file content drifted on a noop reconcile")
	}
}

func TestReconcile_DriftRecovery(t *testing.T) {
	p := mkHosts(t, "127.0.0.1 localhost\n")
	r := newR(p, "x.com")
	if _, err := r.Reconcile(); err != nil {
		t.Fatal(err)
	}
	// Simulate the user nuking the block.
	if err := os.WriteFile(p, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o, err := r.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if !o.Changed || o.Reason != "applied" {
		t.Fatalf("must restore the block, got %+v", o)
	}
	if !strings.Contains(read(t, p), "0.0.0.0 x.com") {
		t.Fatal("block not restored")
	}
}

func TestReconcile_RefusesIfNotRoot(t *testing.T) {
	p := mkHosts(t, "")
	r := &Reconciler{
		HostsPath: p,
		Domains:   []string{"x.com"},
		GetEUID:   func() int { return 501 }, // a normal user
	}
	if _, err := r.Reconcile(); err == nil {
		t.Fatal("must refuse to run as non-root")
	}
	if got := read(t, p); got != "" {
		t.Fatalf("hosts must not be modified, content=%q", got)
	}
}

func TestReconcile_PreservesFileMode(t *testing.T) {
	p := mkHosts(t, "127.0.0.1 localhost\n")
	// chmod to a non-default mode to confirm we preserve it.
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newR(p, "x.com").Reconcile(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode not preserved: %v", got)
	}
}
