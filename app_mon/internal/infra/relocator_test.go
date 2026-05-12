package infra

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFakeBinary writes some bytes to a temp path with 0755 perms.
func writeFakeBinary(t *testing.T, dir, name string, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return p
}

func TestRelocator_Relocate_BasenameIsObfuscated(t *testing.T) {
	home := t.TempDir()
	src := writeFakeBinary(t, t.TempDir(), "appmon", "stub")

	r := NewRelocator(home)
	dst, err := r.Relocate(src)
	if err != nil {
		t.Fatalf("Relocate: %v", err)
	}

	base := filepath.Base(dst)
	if base == "appmon" {
		t.Fatalf("relocated basename should not be 'appmon', got %q", base)
	}
	if strings.Contains(base, "appmon") {
		t.Fatalf("relocated basename should not contain 'appmon', got %q", base)
	}
	if !strings.HasPrefix(base, "com.apple.") {
		t.Fatalf("expected system-looking prefix, got %q", base)
	}

	// Path should not contain "appmon" anywhere — defeats `pkill -f appmon`.
	if strings.Contains(dst, "appmon") {
		t.Fatalf("relocated path should not contain 'appmon', got %q", dst)
	}
}

func TestRelocator_Relocate_ContentMatches(t *testing.T) {
	home := t.TempDir()
	src := writeFakeBinary(t, t.TempDir(), "appmon", "payload-xyz")

	r := NewRelocator(home)
	dst, err := r.Relocate(src)
	if err != nil {
		t.Fatalf("Relocate: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read relocated: %v", err)
	}
	if string(got) != "payload-xyz" {
		t.Fatalf("content mismatch: got %q", string(got))
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat relocated: %v", err)
	}
	if info.Mode().Perm()&0100 == 0 {
		t.Fatalf("relocated file should be executable, mode=%v", info.Mode())
	}
}

func TestRelocator_Relocate_FreshBasenamePerCall(t *testing.T) {
	home := t.TempDir()
	src := writeFakeBinary(t, t.TempDir(), "appmon", "data")
	r := NewRelocator(home)

	seen := map[string]struct{}{}
	for i := 0; i < 6; i++ {
		dst, err := r.Relocate(src)
		if err != nil {
			t.Fatalf("Relocate %d: %v", i, err)
		}
		if _, dup := seen[dst]; dup {
			t.Fatalf("got duplicate relocated path on iteration %d: %s", i, dst)
		}
		seen[dst] = struct{}{}
	}
}

func TestRelocator_CleanStale_RespectsKeepAndMinAge(t *testing.T) {
	home := t.TempDir()
	r := NewRelocator(home)

	// Create three relocated files, manually backdate two of them.
	srcDir := t.TempDir()
	src := writeFakeBinary(t, srcDir, "appmon", "data")

	a, err := r.Relocate(src)
	if err != nil {
		t.Fatalf("Relocate a: %v", err)
	}
	b, err := r.Relocate(src)
	if err != nil {
		t.Fatalf("Relocate b: %v", err)
	}
	c, err := r.Relocate(src)
	if err != nil {
		t.Fatalf("Relocate c: %v", err)
	}

	old := time.Now().Add(-10 * time.Minute)
	for _, p := range []string{a, b, c} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("Chtimes %s: %v", p, err)
		}
	}

	if err := r.CleanStale([]string{a}, 5*time.Minute); err != nil {
		t.Fatalf("CleanStale: %v", err)
	}

	if _, err := os.Stat(a); err != nil {
		t.Fatalf("kept file should still exist: %v", err)
	}
	if _, err := os.Stat(b); !os.IsNotExist(err) {
		t.Fatalf("expected b to be removed, stat err = %v", err)
	}
	if _, err := os.Stat(c); !os.IsNotExist(err) {
		t.Fatalf("expected c to be removed, stat err = %v", err)
	}
}

func TestRelocator_CleanStale_SkipsFreshEntries(t *testing.T) {
	home := t.TempDir()
	r := NewRelocator(home)
	src := writeFakeBinary(t, t.TempDir(), "appmon", "data")

	fresh, err := r.Relocate(src)
	if err != nil {
		t.Fatalf("Relocate: %v", err)
	}

	// minAge=1h, file just created → must not be deleted.
	if err := r.CleanStale(nil, time.Hour); err != nil {
		t.Fatalf("CleanStale: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh file was unexpectedly removed: %v", err)
	}
}

func TestRelocator_CleanStale_MissingDirIsNotError(t *testing.T) {
	r := NewRelocator(t.TempDir())
	if err := r.CleanStale(nil, time.Minute); err != nil {
		t.Fatalf("CleanStale on missing dir: %v", err)
	}
}

func TestRelocateCopy_AtomicallyWritesExecutable(t *testing.T) {
	// Hard link is preferred by Relocate; this exercises the copy fallback
	// directly so cross-filesystem and link-failure paths stay covered.
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	src := writeFakeBinary(t, srcDir, "appmon", "binary-content")
	dst := filepath.Join(dstDir, "com.apple.copy.target")

	if err := relocateCopy(src, dst); err != nil {
		t.Fatalf("relocateCopy: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "binary-content" {
		t.Fatalf("content mismatch: got %q", string(got))
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if info.Mode().Perm()&0100 == 0 {
		t.Fatalf("dst should be executable, got %v", info.Mode())
	}
}

func TestRelocateCopy_FailsOnMissingSource(t *testing.T) {
	dstDir := t.TempDir()
	dst := filepath.Join(dstDir, "target")
	if err := relocateCopy(filepath.Join(t.TempDir(), "nope"), dst); err == nil {
		t.Fatal("expected error on missing source, got nil")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("dst should not exist after failed copy, stat err = %v", err)
	}
}

func TestRelocator_FindProcessesUsingDir_SeesOurOwnExecution(t *testing.T) {
	// `go test` itself runs from a binary outside our relocator dir, so a
	// dir we just created should report no PIDs.
	r := NewRelocator(t.TempDir())
	if err := os.MkdirAll(r.dir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	pids, err := r.FindProcessesUsingDir()
	if err != nil {
		t.Fatalf("FindProcessesUsingDir: %v", err)
	}
	if len(pids) != 0 {
		t.Fatalf("expected no PIDs in empty relocator dir, got %v", pids)
	}
}

func TestParseLegacyAppmonDaemons_MatchesAppmonDaemonRole(t *testing.T) {
	input := strings.Join([]string{
		"  12345 /usr/local/bin/appmon daemon --role watcher --name com.apple.x --mode system",
		"  12346 /usr/local/bin/appmon daemon --role guardian --name com.apple.y --mode system",
	}, "\n")
	pids := parseLegacyAppmonDaemons(input)
	want := []int{12345, 12346}
	if len(pids) != len(want) {
		t.Fatalf("got %v, want %v", pids, want)
	}
	for i, p := range pids {
		if p != want[i] {
			t.Fatalf("pid[%d] = %d, want %d", i, p, want[i])
		}
	}
}

func TestParseLegacyAppmonDaemons_ExcludesCLIInvocations(t *testing.T) {
	// A user typing `appmon start` or `appmon status` must NOT be killed.
	// These look superficially like appmon processes but lack the
	// `daemon --role` argv pattern.
	input := strings.Join([]string{
		"  100 /usr/local/bin/appmon start --mode system",
		"  101 /usr/local/bin/appmon status",
		"  102 /usr/local/bin/appmon update --local-binary ./x",
		"  103 /usr/local/bin/appmon version",
	}, "\n")
	pids := parseLegacyAppmonDaemons(input)
	if len(pids) != 0 {
		t.Fatalf("expected no matches for CLI invocations, got %v", pids)
	}
}

func TestParseLegacyAppmonDaemons_ExcludesRelocatedDaemons(t *testing.T) {
	// Relocated daemons execute from paths under the relocator cache dir.
	// Their basename is the obfuscated name, not "appmon" — they must NOT
	// match this scan, otherwise we'd kill our own healthy daemons. (The
	// watcher's relocator-dir scan handles those.)
	input := strings.Join([]string{
		"  500 /Users/frank.sun/.cache/.com.apple.xpc.6ff7c1a8/com.apple.security.agent.abc123 daemon --role watcher --name com.apple.x --mode system",
	}, "\n")
	pids := parseLegacyAppmonDaemons(input)
	if len(pids) != 0 {
		t.Fatalf("expected no matches for relocated daemons, got %v", pids)
	}
}

func TestParseLiveDaemons_FindsRelocatedAndLegacy(t *testing.T) {
	input := strings.Join([]string{
		"  100 /usr/local/bin/appmon daemon --role guardian --name com.apple.x --mode system",
		"  101 /Users/frank.sun/.cache/.com.apple.xpc.6ff7c1a8/com.apple.cfprefsd.xpc.abc daemon --role watcher --name com.apple.y --mode system",
	}, "\n")
	live := parseLiveDaemons(input)
	if len(live) != 2 {
		t.Fatalf("expected 2 daemons, got %d: %+v", len(live), live)
	}
	if live[0].Role != "guardian" || live[0].PID != 100 {
		t.Fatalf("first daemon mismatched: %+v", live[0])
	}
	if live[1].Role != "watcher" || live[1].PID != 101 {
		t.Fatalf("second daemon mismatched: %+v", live[1])
	}
	if filepath.Base(live[1].Path) != "com.apple.cfprefsd.xpc.abc" {
		t.Fatalf("relocated path basename: got %q", filepath.Base(live[1].Path))
	}
}

func TestParseLiveDaemons_IgnoresCLIAndUnrelatedProcesses(t *testing.T) {
	input := strings.Join([]string{
		"  200 /usr/local/bin/appmon status",       // CLI, no daemon --role
		"  201 /usr/local/bin/appmon start --mode system",
		"  300 /usr/bin/some-unrelated process",
		"  400 /usr/local/bin/git status",
	}, "\n")
	live := parseLiveDaemons(input)
	if len(live) != 0 {
		t.Fatalf("expected no daemons, got %+v", live)
	}
}

func TestExtractRoleArg(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/x daemon --role watcher --name y", "watcher"},
		{"/x daemon --role guardian --mode system", "guardian"},
		{"/x daemon --role", ""},        // no value
		{"/x daemon --name y", ""},      // no --role
		{"/x daemon --role w-1 --mode system", "w-1"},
	}
	for _, tc := range cases {
		got := extractRoleArg(tc.in)
		if got != tc.want {
			t.Errorf("extractRoleArg(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseLegacyAppmonDaemons_ExcludesUnrelatedProcesses(t *testing.T) {
	input := strings.Join([]string{
		"  200 /usr/sbin/cupsd",
		"  201 /usr/local/bin/git status",
		"",
		"  202 /System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/Carbon.framework/Versions/A/Frameworks/SecurityHI.framework/Versions/A/Resources/SecurityAgent",
	}, "\n")
	pids := parseLegacyAppmonDaemons(input)
	if len(pids) != 0 {
		t.Fatalf("expected no matches for unrelated processes, got %v", pids)
	}
}
