package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

// --- pure parsing ---------------------------------------------------------

func TestIsValidDaemonTag(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// happy
		{"daemon-v0.1.0", true},
		{"daemon-v1.2.3", true},
		{"daemon-v10.20.30", true},
		{"daemon-v1.2.3-rc.1", true},
		{"daemon-v1.2.3-beta-foo", true},
		{"daemon-v1.2.3+build.42", true},

		// platform tag (rejected — wrong prefix)
		{"v1.2.3", false},
		// missing prefix
		{"daemon-1.2.3", false},
		{"", false},
		// path traversal
		{"daemon-v/../etc/passwd", false},
		{"daemon-v0.1.0/extra", false},
		{"daemon-v0.1.0 ", false},
		{" daemon-v0.1.0", false},
		{"daemon-v0.1.0\n", false},
		// dangerous pre-release
		{"daemon-v1.2.3-", false},
		{"daemon-v1.2.3-../x", false},
		// wrong prefix capitalization
		{"Daemon-v1.2.3", false},
	}
	for _, c := range cases {
		if got := isValidDaemonTag(c.in); got != c.want {
			t.Errorf("isValidDaemonTag(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseSelfUpdate_RejectsBadTag(t *testing.T) {
	_, code := parseSelfUpdate([]string{"v0.1.0"})
	if code == 0 {
		t.Fatal("missing daemon- prefix must be rejected")
	}
	_, code = parseSelfUpdate([]string{}) // no tag
	if code == 0 {
		t.Fatal("missing positional tag must be rejected")
	}
}

func TestParseSelfUpdate_DefaultsAndOverrides(t *testing.T) {
	o, code := parseSelfUpdate([]string{
		"--release-dir", "/tmp/rel",
		"--dry-run",
		"--keep-old",
		"--healthy-timeout", "5s",
		"--probe-interval", "200ms",
		"--asset-pattern", "daemon-darwin-{arch}",
		"daemon-v0.1.0",
	})
	if code != 0 {
		t.Fatalf("parse failed, code %d", code)
	}
	if o.tag != "daemon-v0.1.0" {
		t.Errorf("tag = %q", o.tag)
	}
	if o.releaseDir != "/tmp/rel" {
		t.Errorf("releaseDir = %q", o.releaseDir)
	}
	if !o.dryRun || !o.keepOld {
		t.Errorf("flags not parsed: dryRun=%v keepOld=%v", o.dryRun, o.keepOld)
	}
	if o.healthyTimeout != 5*time.Second {
		t.Errorf("healthyTimeout = %v", o.healthyTimeout)
	}
	if o.probeInterval != 200*time.Millisecond {
		t.Errorf("probeInterval = %v", o.probeInterval)
	}
	if o.assetPattern != "daemon-darwin-{arch}" {
		t.Errorf("assetPattern = %q", o.assetPattern)
	}
}

// --- end-to-end with --release-dir + offline private key -----------------
//
// This test exercises `doSelfUpdate` against a fake install written to
// a temp HOME, with a locally-signed release served via --release-dir.
// It requires the offline private key (~/.creds/…) or
// $FOCUSD_ED25519_PRIVATE_KEY; otherwise it skips — same convention as
// internal/sig's cross-check test.
//
// The test uses --dry-run to avoid actually shelling launchctl: a real
// `launchctl bootstrap` on a CI macOS runner is unreliable and not
// what we're proving here. (The orchestration itself is covered by
// the osadapter table tests.)
func TestDoSelfUpdate_DryRunEndToEnd(t *testing.T) {
	// Load the offline private key BEFORE we change HOME, since it
	// lives under the operator's real ~/.creds.
	priv := loadOfflinePrivKey()
	if priv == nil {
		t.Skip("offline private key not available; skipping E2E (orchestration is unit-tested separately)")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)

	// Build a "current install": one disguised binary (signed) +
	// three plists under ~/Library/LaunchAgents whose Label shares a
	// base and whose ProgramArguments[0] points at the binary.
	supportRoot := filepath.Join(home, "Library", "Application Support")
	workdir := filepath.Join(supportRoot, ".com.apple.metadata.OLD")
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		t.Fatal(err)
	}
	curBin := filepath.Join(workdir, "com.apple.metadata.helper.OLD")
	writeSigned(t, priv, curBin, []byte("OLD-DAEMON-BYTES"))

	laDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(laDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"a", "b", "ensure"} {
		label := "com.apple.metadata.OLD." + role
		plist := makePlist(label, curBin, workdir)
		if err := os.WriteFile(filepath.Join(laDir, label+".plist"), []byte(plist), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Stage a fake release: <relDir>/<tag>/<asset> with signed bytes.
	relDir := t.TempDir()
	tag := "daemon-v0.1.0"
	asset := "daemon-darwin-" + runtimeArch()
	if err := os.MkdirAll(filepath.Join(relDir, tag), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSigned(t, priv, filepath.Join(relDir, tag, asset), []byte("NEW-DAEMON-BYTES"))

	// Capture stdout so we can assert dry-run printed the expected plan.
	stdout, restore := captureStdout(t)
	defer restore()

	code := doSelfUpdate([]string{
		"--release-dir", relDir,
		"--workdir", workdir,
		"--asset-pattern", "daemon-darwin-{arch}",
		"--dry-run",
		tag,
	})
	if code != 0 {
		t.Fatalf("doSelfUpdate dry-run returned %d", code)
	}

	out := stdout()
	for _, want := range []string{
		"self-update --dry-run",
		"current binary",
		curBin,
		"new binary",
		"intended ops",
		"bootstrap new A, B, ensure",
		"REVERSE order (ensure",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q\n---\n%s", want, out)
		}
	}

	// The dry-run path must have rejected the AMFI same-path collision
	// safety check (which would only fire on a 4-hex collision) — no
	// actual mutation should have happened: launchd unaware, plists +
	// binary still where we put them.
	if _, err := os.Stat(curBin); err != nil {
		t.Errorf("dry-run must not touch old binary: %v", err)
	}
	for _, role := range []string{"a", "b", "ensure"} {
		pp := filepath.Join(laDir, "com.apple.metadata.OLD."+role+".plist")
		if _, err := os.Stat(pp); err != nil {
			t.Errorf("dry-run must not remove old plist %s: %v", pp, err)
		}
	}
}

func TestDoSelfUpdate_RejectsBadTagBeforeIO(t *testing.T) {
	// Even on a clean machine with no install, a bad tag must be
	// rejected before any filesystem or network I/O is attempted.
	t.Setenv("HOME", t.TempDir())
	code := doSelfUpdate([]string{"v0.1.0"})
	if code == 0 {
		t.Fatal("missing daemon- prefix must be rejected")
	}
	code = doSelfUpdate([]string{"daemon-v/../etc/passwd"})
	if code == 0 {
		t.Fatal("path-traversal in tag must be rejected")
	}
}

// --- helpers --------------------------------------------------------------

// loadOfflinePrivKey returns the focusd offline Ed25519 PKCS#8 PEM
// private key, or nil if it isn't available. Caller MUST invoke this
// before any t.Setenv("HOME", …) — once HOME is rewritten,
// os.UserHomeDir() returns the wrong path.
func loadOfflinePrivKey() []byte {
	if v := os.Getenv("FOCUSD_ED25519_PRIVATE_KEY"); v != "" {
		return []byte(v)
	}
	if h, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(h, ".creds", "focusd_ed25519_private.pem")
		if b, err := os.ReadFile(p); err == nil {
			return b
		}
	}
	return nil
}

func writeSigned(t *testing.T, privPEM []byte, outPath string, prog []byte) {
	t.Helper()
	inPath := outPath + ".unsigned"
	if err := os.WriteFile(inPath, prog, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(inPath)
	if err := sig.SignFile(inPath, outPath, privPEM); err != nil {
		t.Fatalf("sign %s: %v", outPath, err)
	}
}

func makePlist(label, bin, workdir string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>` + label + `</string>
  <key>ProgramArguments</key><array>
    <string>` + bin + `</string>
    <string>run</string>
    <string>--workdir</string>
    <string>` + workdir + `</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict></plist>
`
}

func runtimeArch() string {
	return runtime.GOARCH
}

func captureStdout(t *testing.T) (read func() string, restore func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	doneCh := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if err != nil {
				doneCh <- sb.String()
				return
			}
		}
	}()
	read = func() string {
		w.Close()
		return <-doneCh
	}
	restore = func() { os.Stdout = orig }
	return
}
