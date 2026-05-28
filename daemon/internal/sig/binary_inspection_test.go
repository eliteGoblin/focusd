package sig

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestBuiltDaemonHasNoPEMLiterals builds the daemon binary in a temp
// directory and confirms that the literal PEM markers do not appear in
// its byte image. This is the highest-signal regression test for the
// grep-resistance change: if anyone re-introduces `//go:embed
// focusd_ed25519_public.pem` the binary will start leaking again and
// this test will fail.
//
// Skipped where building isn't realistic (e.g. CI without a Go toolchain
// on PATH, or no module access).
func TestBuiltDaemonHasNoPEMLiterals(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping daemon build")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain on PATH")
	}

	// daemon module root is two dirs up from this test file
	// (daemon/internal/sig -> daemon).
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	daemonRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))

	dir := t.TempDir()
	out := filepath.Join(dir, "daemon")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/daemon")
	cmd.Dir = daemonRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if buildOut, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("go build daemon: %v\n%s", err, buildOut)
	}

	bin, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read built daemon: %v", err)
	}
	for _, needle := range [][]byte{
		[]byte("BEGIN PUBLIC KEY"),
		[]byte("END PUBLIC KEY"),
	} {
		if bytes.Contains(bin, needle) {
			t.Errorf("built daemon binary still contains literal %q "+
				"— the pubkey embed regressed", needle)
		}
	}
}
