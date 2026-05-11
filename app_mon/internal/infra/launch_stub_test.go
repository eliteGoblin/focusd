package infra

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeStubBinary creates an executable file with the given content and
// returns its path.
func writeStubBinary(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0755))
	return p
}

func TestEnsureLaunchStub_FreshCreatesStubAndPersists(t *testing.T) {
	home := t.TempDir()
	src := writeStubBinary(t, t.TempDir(), "appmon", "payload-v1")

	reg, _ := newTestRegistry(t)

	stub, err := EnsureLaunchStub(src, home, reg)
	require.NoError(t, err)

	// Stub exists at a randomized basename under the relocator dir.
	r := NewRelocator(home)
	require.True(t, strings.HasPrefix(stub, r.Dir()+string(os.PathSeparator)),
		"stub %q should live under %q", stub, r.Dir())
	require.NotEqual(t, "appmon", filepath.Base(stub))

	// Path is persisted in the SecretStore.
	stored, err := reg.GetSecret(SecretKeyLaunchStub)
	require.NoError(t, err)
	require.Equal(t, stub, stored)

	// Content matches source.
	got, err := os.ReadFile(stub)
	require.NoError(t, err)
	require.Equal(t, "payload-v1", string(got))
}

func TestEnsureLaunchStub_ReusesStubWhenContentMatches(t *testing.T) {
	home := t.TempDir()
	src := writeStubBinary(t, t.TempDir(), "appmon", "payload")
	reg, _ := newTestRegistry(t)

	first, err := EnsureLaunchStub(src, home, reg)
	require.NoError(t, err)

	second, err := EnsureLaunchStub(src, home, reg)
	require.NoError(t, err)

	require.Equal(t, first, second, "stub path should be reused when content unchanged")
}

func TestEnsureLaunchStub_RefreshesWhenSourceChanges(t *testing.T) {
	home := t.TempDir()
	srcDir := t.TempDir()
	src := writeStubBinary(t, srcDir, "appmon", "payload-v1")
	reg, _ := newTestRegistry(t)

	first, err := EnsureLaunchStub(src, home, reg)
	require.NoError(t, err)

	// Simulate an `appmon update` install step: atomic rename gives the
	// source a NEW inode. The hard-link stub points at the OLD inode and
	// is therefore stale, so EnsureLaunchStub must regenerate it.
	replacement := writeStubBinary(t, srcDir, "appmon.next", "payload-v2")
	require.NoError(t, os.Rename(replacement, src))

	second, err := EnsureLaunchStub(src, home, reg)
	require.NoError(t, err)

	require.NotEqual(t, first, second, "stub should be regenerated when source content changes")

	got, err := os.ReadFile(second)
	require.NoError(t, err)
	require.Equal(t, "payload-v2", string(got))

	// Old stub path should be cleaned up.
	_, err = os.Stat(first)
	require.True(t, os.IsNotExist(err), "old stub should be unlinked, stat err = %v", err)
}

func TestStubMatches(t *testing.T) {
	dir := t.TempDir()
	a := writeStubBinary(t, dir, "a", "same")
	b := writeStubBinary(t, dir, "b", "same")
	c := writeStubBinary(t, dir, "c", "different")

	require.True(t, stubMatches(a, b), "identical content should match")
	require.False(t, stubMatches(a, c), "different content should not match")
	require.False(t, stubMatches(a, filepath.Join(dir, "missing")),
		"missing target should not match")
	require.False(t, stubMatches(filepath.Join(dir, "missing"), a),
		"missing stub should not match")
}

func TestRelocator_Dir_ReturnsConfiguredPath(t *testing.T) {
	home := t.TempDir()
	r := NewRelocator(home)
	require.Equal(t, relocatorDir(home), r.Dir())
}
