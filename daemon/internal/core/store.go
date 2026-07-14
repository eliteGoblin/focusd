package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/eliteGoblin/focusd/daemon/internal/relocate"
)

// Store is the daemon's tiny on-disk state under the workdir. The
// daemon process is stateless; durable facts live in these files and
// are re-read every tick (kubelet : etcd shape).
//
// FEATURE 21 (HF1): the daemon's own state and the platform's disposable
// binaries now live under SEPARATE roots so a platform-workdir wipe can't take
// the daemon's identity/state down with it. Dir is the daemon-home (survives a
// wipe); PlatformDir, when set, is the disposable platform-workdir where the
// platform binaries live. PlatformDir empty ⇒ BinPath falls back to Dir (the
// legacy single-root layout, still used by unit/e2e tests and non-mesh runs).
//
//	<Dir>/version.json         {"desired":"v1"}   desired version   (daemon-home)
//	<Dir>/good                 "v1"               last-known-good   (daemon-home)
//	<Dir>/bad/<v>              (marker file)      crash-looped      (daemon-home)
//	<Dir>/.roster              (masked labels)    mesh roster       (daemon-home)
//	<platformRoot>/bin/<v>/platform               platform binaries (platform-workdir)
type Store struct {
	Dir string
	// PlatformDir is the disposable platform-workdir root for platform
	// binaries (bin/<v>/platform). Empty ⇒ use Dir (legacy single root).
	PlatformDir string
}

// platformRoot is where the platform binaries live: the separate
// platform-workdir when set, else the daemon-home (legacy single-root).
func (s *Store) platformRoot() string {
	if s.PlatformDir != "" {
		return s.PlatformDir
	}
	return s.Dir
}

// VersionFile is the basename of the desired-version config under the workdir.
// Exported so callers that must locate it (e.g. status install-age) reference
// this single source of truth rather than hardcoding the literal.
const VersionFile = "version.json"

// RosterFile is the basename of the masked mesh-label roster under the
// workdir (FEATURE 10 / ADR-0014). It holds the three independent mesh
// labels XOR-masked so a casual `cat` shows non-plaintext bytes; a
// freshly relaunched survivor reads it to recover the roster on a cold
// start. In-memory roster is authoritative; this file self-heals from it.
const RosterFile = ".roster"

// InstallSaltFile is the basename (in the daemon-home) of the per-install salt
// that seeds every HF4 (FEATURE 24) disguise derivation: the platform binary's
// on-disk basename (BinPath) and the platform child's argv[0] (PlatformArgv0).
// It IS the "version→path index the daemon reads": salt + version → path,
// deterministically, so status/procCount reconstructs the exact running argv
// without a second lookup table. Neutral, dot-hidden basename; lives 0700 in the
// disguised daemon-home. Absent ⇒ the legacy non-disguised layout (dev/tests).
const InstallSaltFile = ".idx"

type versionConfig struct {
	Desired string `json:"desired"`
}

func (s *Store) versionPath() string { return filepath.Join(s.Dir, VersionFile) }
func (s *Store) goodPath() string    { return filepath.Join(s.Dir, "good") }
func (s *Store) badDir() string      { return filepath.Join(s.Dir, "bad") }

// RosterPath is the absolute path of the masked mesh-label roster file
// under the workdir. Exported so the osadapter mesh layer can read/write
// it via the core roster helpers.
func (s *Store) RosterPath() string { return filepath.Join(s.Dir, RosterFile) }

// saltPath is the absolute path of the per-install disguise salt (daemon-home).
func (s *Store) saltPath() string { return filepath.Join(s.Dir, InstallSaltFile) }

// InstallSalt returns the per-install disguise salt, or "" if none has been
// written yet (dev runs and the whole existing unit/e2e test corpus, which never
// seed it — so they keep the legacy bin/<v>/platform layout unchanged).
func (s *Store) InstallSalt() string {
	b, err := os.ReadFile(s.saltPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// EnsureInstallSalt returns the per-install salt, generating + persisting a fresh
// 16-byte random one on first call (atomic write, 0600). Idempotent: once written
// it is stable for the install's lifetime, so every daemon role and the status
// subcommand derive the SAME disguised platform paths/argv. Called by the
// reconcile-loop composition root; a write failure degrades to "" (legacy layout)
// rather than blocking protection.
func (s *Store) EnsureInstallSalt() (string, error) {
	if v := s.InstallSalt(); v != "" {
		return v, nil
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	salt := hex.EncodeToString(buf)
	if err := atomicWrite(s.saltPath(), []byte(salt)); err != nil {
		return "", err
	}
	// Tighten to 0600: the salt is the seed an attacker would need to map a
	// disguised platform path back to a version. atomicWrite lands 0644.
	_ = os.Chmod(s.saltPath(), 0o600)
	return salt, nil
}

// BinPath is where the platform binary for version v lives — under the
// platform-workdir (disposable) when PlatformDir is set, else under Dir.
//
// HF4 (FEATURE 24): with a per-install salt present the binary lives at
// bin/<disguised-basename> (relocate.PlatformBinBase) — no 'platform' literal, no
// version in the path. Without a salt (dev/tests) it falls back to the legacy
// bin/<v>/platform. Both are pure functions of (salt, v), so every caller —
// executor, fetch, status, watchdog — agrees on the path with no shared index.
func (s *Store) BinPath(v string) string {
	if base := relocate.PlatformBinBase(s.InstallSalt(), v); base != "" {
		return filepath.Join(s.platformRoot(), "bin", base)
	}
	return filepath.Join(s.platformRoot(), "bin", v, "platform")
}

// PlatformArgv0 is the deterministic disguised argv[0] the daemon sets on the
// platform child (HF4). Empty when no salt is present (legacy: the child keeps
// its binary path as argv[0]). Derived from the salt so status/procCount can
// rebuild the exact argv to match the running process.
func (s *Store) PlatformArgv0() string {
	return relocate.PlatformArgv0(s.InstallSalt())
}

// LockPath is the singleton-lock file the winning daemon holds for the
// lifetime of its platform child. fd-tied advisory lock ⇒ kernel auto-
// releases on holder death, so a standby's next tick takes over.
func (s *Store) LockPath() string { return filepath.Join(s.Dir, "platform.lock") }

// HaveBin reports whether the platform binary for v exists.
func (s *Store) HaveBin(v string) bool {
	fi, err := os.Stat(s.BinPath(v))
	return err == nil && !fi.IsDir()
}

// HaveConfig reports whether a desired version has been resolved.
func (s *Store) HaveConfig() bool {
	_, err := os.Stat(s.versionPath())
	return err == nil
}

// Desired returns the configured desired version ("" if none).
func (s *Store) Desired() string {
	b, err := os.ReadFile(s.versionPath())
	if err != nil {
		return ""
	}
	var c versionConfig
	if json.Unmarshal(b, &c) != nil {
		return ""
	}
	return c.Desired
}

// WriteDesired atomically records the desired version.
func (s *Store) WriteDesired(v string) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(versionConfig{Desired: v})
	return atomicWrite(s.versionPath(), b)
}

// Good / WriteGood track the last-known-good version.
func (s *Store) Good() string {
	b, err := os.ReadFile(s.goodPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (s *Store) WriteGood(v string) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	return atomicWrite(s.goodPath(), []byte(v))
}

// MarkBad records v as crash-looped (never run again).
func (s *Store) MarkBad(v string) error {
	if err := os.MkdirAll(s.badDir(), 0o755); err != nil {
		return err
	}
	// Filename is path-sanitised; the ORIGINAL version is the file
	// CONTENT so BadSet returns the exact version string and the
	// executor's s.Bad[desired] lookup is symmetric (no silent miss
	// for versions containing sanitised characters).
	return atomicWrite(filepath.Join(s.badDir(), safe(v)), []byte(v))
}

// BadSet returns the exact versions marked bad (read from file
// content, so lookups by the original version string always match).
func (s *Store) BadSet() map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(s.badDir())
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(s.badDir(), e.Name()))
		if rerr != nil {
			continue
		}
		if v := strings.TrimSpace(string(b)); v != "" {
			out[v] = true
		}
	}
	return out
}

func safe(v string) string {
	return strings.NewReplacer("/", "_", "..", "_", " ", "_").Replace(v)
}

// atomicWrite writes via temp + rename so a crash mid-write cannot
// corrupt state (the next tick repairs anyway).
func atomicWrite(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
