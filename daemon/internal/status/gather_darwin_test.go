//go:build darwin

package status

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
	"github.com/eliteGoblin/focusd/daemon/internal/platdir"
	"github.com/eliteGoblin/focusd/daemon/internal/status/redact"
)

// okVerify is a pass-through signature seam for tests whose fake platform binary
// is unsigned (the offline signing key is not in CI). Production wires
// sig.VerifyFile.
func okVerify(string) (bool, error) { return true, nil }

// TestRunPlatformStatus_ExecFailure: a non-existent binary yields ran=false
// (not a panic, not a leak), which drives PlatformDetail unavailable upstream.
func TestRunPlatformStatus_ExecFailure(t *testing.T) {
	out, _, ran := runPlatformStatus(filepath.Join(t.TempDir(), "no-such-binary"), t.TempDir(), false, okVerify)
	if ran {
		t.Fatalf("exec failure should yield ran=false")
	}
	if out != "" {
		t.Fatalf("exec failure should yield empty output, got %q", out)
	}
}

// TestRunPlatformStatus_Exit1IsRan: `platform status` exits 1 on DEGRADED but
// STILL produces valid output. The daemon must treat exit 1 as a successful
// run (ran=true, exitCode=1) so it can surface the degradation — not discard
// it as "unavailable" (BUG 1).
func TestRunPlatformStatus_Exit1IsRan(t *testing.T) {
	bin := writeFakePlatform(t, 1, "degraded-report\n")
	out, code, ran := runPlatformStatus(bin, t.TempDir(), false, okVerify)
	if !ran {
		t.Fatalf("exit 1 (degraded) must read as ran=true")
	}
	if code != 1 {
		t.Fatalf("exitCode = %d; want 1", code)
	}
	if out != "degraded-report\n" {
		t.Fatalf("exit-1 output discarded: got %q", out)
	}
}

// TestRunPlatformStatus_Exit2IsUnavailable: exit code >= 2 is an internal/
// usage failure of the platform itself (not a health verdict) → unavailable.
func TestRunPlatformStatus_Exit2IsUnavailable(t *testing.T) {
	bin := writeFakePlatform(t, 2, "junk\n")
	out, code, ran := runPlatformStatus(bin, t.TempDir(), false, okVerify)
	if ran {
		t.Fatalf("exit 2 must read as ran=false (unavailable)")
	}
	if code != 2 {
		t.Fatalf("exitCode = %d; want 2", code)
	}
	if out != "" {
		t.Fatalf("exit-2 output should be dropped, got %q", out)
	}
}

// TestRunPlatformStatus_RejectsUnverifiedBinary is the HF1 HIGH regression: the
// binary path flows from the (attacker-writable) pointer file into exec, so a
// binary that FAILS the Ed25519 signature check must be refused BEFORE exec —
// never run. With a rejecting verifier, ran=false and the fake binary is never
// executed (its side-effect marker is never created).
func TestRunPlatformStatus_RejectsUnverifiedBinary(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran-marker")
	bin := filepath.Join(t.TempDir(), "planted-platform")
	// If ever executed, this fake would create the marker file.
	script := "#!/bin/sh\ntouch '" + marker + "'\nprintf 'pwned'\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	reject := func(string) (bool, error) { return false, nil }
	out, _, ran := runPlatformStatus(bin, t.TempDir(), false, reject)
	if ran {
		t.Fatal("a signature-rejected binary must NOT run (ran=true)")
	}
	if out != "" {
		t.Fatalf("rejected binary must yield empty output, got %q", out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("REGRESSION: the unverified binary was EXECUTED (marker created)")
	}
}

// TestPlatformStore_RejectsUnsafePointer is the HF1 HIGH regression on the READ
// side: status runs the pointer target through platdir.SafeTarget and, when the
// pointer is unsafe (escapes the support root), falls back to the daemon-home
// rather than steering the store/exec off a hostile path.
func TestPlatformStore_RejectsUnsafePointer(t *testing.T) {
	root := t.TempDir()
	daemonHome := filepath.Join(root, "daemon-home")
	if err := os.MkdirAll(daemonHome, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir() // OUTSIDE the daemon-home's support root
	if err := platdir.Write(daemonHome, outside); err != nil {
		t.Fatal(err)
	}
	st, platWD := platformStore(daemonHome)
	if platWD != daemonHome {
		t.Fatalf("unsafe pointer must fall back to daemon-home, got platWD=%q", platWD)
	}
	if st.PlatformDir != "" {
		t.Fatalf("unsafe pointer must not set PlatformDir, got %q", st.PlatformDir)
	}
}

// TestPlatformStore_HonorsSafePointer: a pointer target that is a safe sibling
// under the support root IS used as the platform-workdir (PlatformDir set).
func TestPlatformStore_HonorsSafePointer(t *testing.T) {
	root := t.TempDir()
	daemonHome := filepath.Join(root, "daemon-home")
	if err := os.MkdirAll(daemonHome, 0o700); err != nil {
		t.Fatal(err)
	}
	platWDdir := filepath.Join(root, ".platform-wd")
	if err := os.MkdirAll(platWDdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := platdir.Write(daemonHome, platWDdir); err != nil {
		t.Fatal(err)
	}
	st, platWD := platformStore(daemonHome)
	if platWD != platWDdir {
		t.Fatalf("safe pointer should be honored, got %q want %q", platWD, platWDdir)
	}
	if st.PlatformDir != platWDdir {
		t.Fatalf("PlatformDir = %q, want %q", st.PlatformDir, platWDdir)
	}
}

// writeFakePlatform writes a tiny executable shell script that prints the
// given stdout and exits with the given code, used to exercise the exit-code
// handling of runPlatformStatus without a real platform binary.
func writeFakePlatform(t *testing.T, exitCode int, stdout string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-platform")
	script := "#!/bin/sh\nprintf '%s' '" + stdout + "'\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestGatherPlatform_NoGoodVersion: with a readable workdir but no good
// version recorded, there's no platform process to query → unavailable.
func TestGatherPlatform_NoGoodVersion(t *testing.T) {
	wd := t.TempDir()
	pd := gatherPlatform(redact.New(wd), false, okVerify)
	if pd.Available {
		t.Fatalf("expected unavailable platform detail when no good version exists")
	}
}

// TestGatherPlatform_AbsentWorkdirToken: an absent token short-circuits to
// unavailable without touching exec.
func TestGatherPlatform_AbsentWorkdirToken(t *testing.T) {
	pd := gatherPlatform(redact.Token{}, false, okVerify)
	if pd.Available {
		t.Fatalf("absent workdir token must yield unavailable")
	}
}

// TestReadVersions_ReadableNoGood: a readable workdir with a desired but no
// good file reports good="" and vUnknown=false (genuine state, NOT unknown).
func TestReadVersions_ReadableNoGood(t *testing.T) {
	wd := t.TempDir()
	st := &core.Store{Dir: wd}
	if err := st.WriteDesired("v1.0.0"); err != nil {
		t.Fatal(err)
	}
	desired, good, vUnknown := readVersions(redact.New(wd))
	if desired != "v1.0.0" {
		t.Errorf("desired = %q; want v1.0.0", desired)
	}
	if good != "" {
		t.Errorf("good = %q; want empty", good)
	}
	if vUnknown {
		t.Errorf("vUnknown = true; want false (readable workdir, just no good yet)")
	}
}

// TestReadVersions_AbsentWorkdir: a non-existent workdir is not an error
// (vUnknown stays false — nothing to report), distinct from EACCES.
func TestReadVersions_AbsentWorkdir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, good, vUnknown := readVersions(redact.New(missing))
	if good != "" || vUnknown {
		t.Errorf("absent workdir: good=%q vUnknown=%v; want empty,false", good, vUnknown)
	}
}

// TestInstallAge_FromVersionJSON: warming-up detection derives age from
// version.json mtime; a freshly written file reads as young.
func TestInstallAge_FromVersionJSON(t *testing.T) {
	wd := t.TempDir()
	if err := os.WriteFile(filepath.Join(wd, "version.json"), []byte(`{"desired":"v1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	age, ok := installAge(redact.New(wd))
	if !ok {
		t.Fatal("expected installAge ok=true for an existing version.json")
	}
	if age > warmupWindow {
		t.Errorf("fresh version.json age = %s; want < %s", age, warmupWindow)
	}
}
