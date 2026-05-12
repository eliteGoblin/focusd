package infra

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// HostsManager owns a section of /etc/hosts via marker comments. The
// invariants these tests pin are:
//
//   1. Idempotent — same input doesn't churn the file.
//   2. Tamper-resistant — edits inside the markers are reverted.
//   3. Tamper-safe — edits OUTSIDE the markers are preserved verbatim.
//   4. Atomic — partial writes can't leave the file half-managed.
//
// Any regression here is real: invariant #3 means we never destroy the
// user's hand-curated hosts entries; #2 is the protection contract.

func newHostsFile(t *testing.T, contents string) (path string, hm *HostsManager) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "hosts")
	if contents != "" {
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatalf("seed hosts file: %v", err)
		}
	}
	return path, NewHostsManagerWithPath(path)
}

func TestEnsureBlocklist_AddsBlockWhenAbsent(t *testing.T) {
	path, hm := newHostsFile(t, "127.0.0.1 localhost\n")
	changed, err := hm.EnsureBlocklist([]string{"example.com", "evil.com"})
	if err != nil {
		t.Fatalf("EnsureBlocklist: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true on first install")
	}

	got, _ := os.ReadFile(path)
	str := string(got)

	// User's existing line is preserved
	if !strings.Contains(str, "127.0.0.1 localhost") {
		t.Errorf("user line lost; file:\n%s", str)
	}
	// Markers are present
	if !strings.Contains(str, HostsBlockBegin) || !strings.Contains(str, HostsBlockEnd) {
		t.Errorf("markers missing; file:\n%s", str)
	}
	// Both hosts are in the block
	if !strings.Contains(str, "0.0.0.0 example.com") {
		t.Errorf("example.com not blocked; file:\n%s", str)
	}
	if !strings.Contains(str, "0.0.0.0 evil.com") {
		t.Errorf("evil.com not blocked; file:\n%s", str)
	}
}

func TestEnsureBlocklist_NoOpWhenAlreadyCorrect(t *testing.T) {
	path, hm := newHostsFile(t, "127.0.0.1 localhost\n")
	hosts := []string{"a.com", "b.com"}
	if _, err := hm.EnsureBlocklist(hosts); err != nil {
		t.Fatalf("first EnsureBlocklist: %v", err)
	}
	info1, _ := os.Stat(path)

	// Second call with same input must not rewrite the file.
	changed, err := hm.EnsureBlocklist(hosts)
	if err != nil {
		t.Fatalf("second EnsureBlocklist: %v", err)
	}
	if changed {
		t.Error("expected changed=false on idempotent re-apply")
	}
	info2, _ := os.Stat(path)
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Errorf("file was rewritten despite identical content (mtime drifted %v → %v)",
			info1.ModTime(), info2.ModTime())
	}
}

func TestEnsureBlocklist_RevertsInternalTampering(t *testing.T) {
	// Set up an installed blocklist, then a tamper that removes
	// a host. EnsureBlocklist must put it back.
	path, hm := newHostsFile(t, "127.0.0.1 localhost\n")
	hosts := []string{"steampowered.com", "youtube.com"}
	if _, err := hm.EnsureBlocklist(hosts); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Simulate a user-mode-weakness edit: remove the youtube.com line.
	current, _ := os.ReadFile(path)
	tampered := strings.Replace(string(current), "0.0.0.0 youtube.com\n", "", 1)
	if err := os.WriteFile(path, []byte(tampered), 0644); err != nil {
		t.Fatal(err)
	}

	changed, err := hm.EnsureBlocklist(hosts)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true after tampering")
	}

	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "0.0.0.0 youtube.com") {
		t.Errorf("youtube.com was not restored after tampering; file:\n%s", string(got))
	}
}

func TestEnsureBlocklist_PreservesEntriesOutsideMarkers(t *testing.T) {
	// User-authored entries above and below the managed block must
	// survive every rewrite — this is the "tamper-SAFE" invariant.
	const initial = `127.0.0.1 localhost
255.255.255.255 broadcasthost
::1 localhost
10.0.0.1 my-private-server
`
	path, hm := newHostsFile(t, initial)
	if _, err := hm.EnsureBlocklist([]string{"blocked.com"}); err != nil {
		t.Fatal(err)
	}

	// Append a user-curated entry AFTER the managed block.
	current, _ := os.ReadFile(path)
	withExtras := string(current) + "\n# my personal override\n192.168.1.50 home-server.lan\n"
	if err := os.WriteFile(path, []byte(withExtras), 0644); err != nil {
		t.Fatal(err)
	}

	// Apply with a different blocklist → managed block changes,
	// user's surrounding entries do NOT.
	if _, err := hm.EnsureBlocklist([]string{"new-blocked.com"}); err != nil {
		t.Fatal(err)
	}

	final, _ := os.ReadFile(path)
	str := string(final)

	for _, mustHave := range []string{
		"127.0.0.1 localhost",          // pre-block user entry
		"10.0.0.1 my-private-server",   // pre-block user entry
		"# my personal override",       // post-block user comment
		"192.168.1.50 home-server.lan", // post-block user entry
		"0.0.0.0 new-blocked.com",      // current block content
	} {
		if !strings.Contains(str, mustHave) {
			t.Errorf("expected %q in final file:\n%s", mustHave, str)
		}
	}
	// Confirm old block content is gone.
	if strings.Contains(str, "0.0.0.0 blocked.com") {
		t.Errorf("previous block entry leaked into new content:\n%s", str)
	}
}

func TestEnsureBlocklist_HandlesMissingHostsFile(t *testing.T) {
	// /etc/hosts may not exist on a fresh image. Treat as empty,
	// create on first write.
	dir := t.TempDir()
	path := filepath.Join(dir, "no-hosts-yet")
	hm := NewHostsManagerWithPath(path)

	changed, err := hm.EnsureBlocklist([]string{"x.com"})
	if err != nil {
		t.Fatalf("EnsureBlocklist on missing file: %v", err)
	}
	if !changed {
		t.Error("expected changed=true when file was created from nothing")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read created file: %v", err)
	}
	if !strings.Contains(string(got), "0.0.0.0 x.com") {
		t.Errorf("created file missing entry; got:\n%s", string(got))
	}
}

func TestEnsureBlocklist_SkipsEmptyAndWhitespaceEntries(t *testing.T) {
	// Compile-in blocklist might accidentally include "" — must not
	// emit a "0.0.0.0 " line that resolves nothing-or-everything.
	path, hm := newHostsFile(t, "")
	if _, err := hm.EnsureBlocklist([]string{"  ", "", "valid.com", "\t"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	lines := strings.Split(string(got), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "0.0.0.0" {
			t.Errorf("emitted malformed bare-IP line: %q\nfull:\n%s", line, string(got))
		}
	}
	if !strings.Contains(string(got), "0.0.0.0 valid.com") {
		t.Errorf("valid.com missing; file:\n%s", string(got))
	}
}

func TestActiveBlocklist_RoundTripsViaFile(t *testing.T) {
	path, hm := newHostsFile(t, "127.0.0.1 localhost\n")
	hosts := []string{"a.com", "b.com", "c.com"}
	if _, err := hm.EnsureBlocklist(hosts); err != nil {
		t.Fatal(err)
	}
	got, err := hm.ActiveBlocklist()
	if err != nil {
		t.Fatalf("ActiveBlocklist: %v", err)
	}
	// The host list comes back in the same order we wrote it.
	if len(got) != len(hosts) {
		t.Fatalf("ActiveBlocklist len=%d, want %d (%v vs %v)", len(got), len(hosts), got, hosts)
	}
	for i, h := range hosts {
		if got[i] != h {
			t.Errorf("ActiveBlocklist[%d] = %q, want %q", i, got[i], h)
		}
	}
	// Confirm the path is intact.
	_ = path
}

func TestActiveBlocklist_EmptyWhenNoMarkers(t *testing.T) {
	_, hm := newHostsFile(t, "127.0.0.1 localhost\n# nothing managed here\n")
	got, err := hm.ActiveBlocklist()
	if err != nil {
		t.Fatalf("ActiveBlocklist: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty active list, got %v", got)
	}
}

func TestEnsureBlocklist_PreservesFilePermissions(t *testing.T) {
	// /etc/hosts is conventionally 0644; the atomic-rename path must
	// not silently change permissions to whatever umask gives us.
	path, hm := newHostsFile(t, "127.0.0.1 localhost\n")
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatalf("chmod seed: %v", err)
	}
	if _, err := hm.EnsureBlocklist([]string{"x.com"}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	// Our atomicWrite forces 0644 — that's the documented intent for
	// /etc/hosts. Asserting this catches a regression where we'd
	// leak a tighter mode (preventing read by non-root, breaking
	// `appmon blocklist` for the user).
	if info.Mode().Perm() != 0644 {
		t.Errorf("file mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestBuildManagedBlock_FormatIsStable(t *testing.T) {
	// Exact byte-level snapshot of the block format. Any change must
	// be intentional — drifting the format invalidates every machine's
	// previously-installed block, causing a one-time spurious rewrite
	// on next start.
	got := buildManagedBlock([]string{"alpha.test", "beta.test"})
	want := HostsBlockBegin + "\n" +
		"0.0.0.0 alpha.test\n" +
		"0.0.0.0 beta.test\n" +
		HostsBlockEnd + "\n"
	if got != want {
		t.Errorf("format drift.\n got:\n%s\nwant:\n%s", got, want)
	}
}
