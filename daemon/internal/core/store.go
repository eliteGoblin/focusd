package core

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// RosterFile is the LEGACY fixed basename of the masked mesh-label roster
// (FEATURE 10 / ADR-0014). FEATURE 26 salt-derives the live basename
// (RosterPath); this literal is the fallback for dev/test/legacy (no salt). It
// holds the three independent mesh labels XOR-masked so a casual `cat` shows
// non-plaintext bytes; a freshly relaunched survivor reads it to recover the
// roster on a cold start. In-memory roster is authoritative; it self-heals.
const RosterFile = ".roster"

// InstallSaltFile is the basename (in the daemon-home) of the per-install salt
// that seeds every disguise derivation: the platform binary's on-disk basename
// (BinPath), the platform child's argv[0] (PlatformArgv0), and — FEATURE 26 — the
// masked-roster / pidfile / pointer / lock basenames. It IS the single FIXED
// literal (relocate.SaltBasename) a reader needs to bootstrap the salt before it
// can derive the rest. Neutral, dot-hidden; lives 0700 in the disguised
// daemon-home. Absent ⇒ the legacy non-disguised layout (dev/tests).
const InstallSaltFile = relocate.SaltBasename

// PlatformPidFile is the LEGACY fixed basename (in the daemon-home) of the
// platform child's liveness pidfile (HF4 FEATURE 24, P3). FEATURE 26 salt-derives
// the live basename (PidFilePath); this literal is the fallback for dev/test/
// legacy. It holds ONLY the child's OS pid as a bare integer — no path, no
// version, no greppable word — so status liveness is SALT-INDEPENDENT: a `focusd
// status` CLI reads it and probes the pid directly, correct even if the disguise
// salt diverged from the running child's argv.
const PlatformPidFile = ".seq"

type versionConfig struct {
	Desired string `json:"desired"`
}

// FEATURE 26 (bundle 4) — version grep-hook mask.
//
// version.json / good / bad/* CONTENT carried the platform version in plaintext,
// and a bad/<v> FILENAME carried it too — so a `grep -r v0.16 ~/Library` or a
// `find -name 'v0.16*'` hit the install even after HF4 disguised the bin path.
// These are now masked with a distinct salt-keyed XOR key, and the bad/<v>
// filename becomes a keyed digest. Legacy plaintext files are still ACCEPTED on
// read (a leading marker byte disambiguates masked from plaintext), so an upgrade
// self-heals to masked on the next write without losing the current version.
//
// Empty salt (dev/test/legacy) ⇒ no mask, legacy filenames — the deterministic
// layout the existing tests and e2e rely on is unchanged.

// verMaskMarker is the non-printable leading byte of a masked version payload. A
// correctly un-masked payload starts with it; a legacy plaintext file un-masked
// with the key almost never does, so its absence reliably flags "legacy
// plaintext" (deterministic, not a probabilistic heuristic).
const verMaskMarker byte = 0x1e

// verMaskKey is the deterministic XOR key for version-state content, distinct
// from every other salt-keyed mask. Empty salt ⇒ nil (no masking).
func (s *Store) verMaskKey() []byte {
	salt := s.InstallSalt()
	if salt == "" {
		return nil
	}
	h := sha256.Sum256([]byte(salt + "|verstate"))
	return h[:]
}

// maskVer returns the on-disk bytes for version-state data: XOR(marker+data, key)
// when a salt is present, else data verbatim (legacy/test plaintext).
func (s *Store) maskVer(data []byte) []byte {
	key := s.verMaskKey()
	if key == nil {
		return data
	}
	return xor(append([]byte{verMaskMarker}, data...), key)
}

// unmaskVer returns (data, true) when raw is a file WE masked (marker present
// after un-masking), or (raw, false) when it is a legacy plaintext file the
// caller should use as-is. With no salt it returns (raw, false) — plaintext is
// authoritative.
func (s *Store) unmaskVer(raw []byte) ([]byte, bool) {
	key := s.verMaskKey()
	if key == nil {
		return raw, false
	}
	u := xor(raw, key)
	if len(u) >= 1 && u[0] == verMaskMarker {
		return u[1:], true
	}
	return raw, false // not ours-masked → treat as legacy plaintext
}

// badName is the bad-marker basename for version v: a keyed HMAC digest (no
// version leak in the filename) when a salt is present, else the legacy
// path-sanitised name. Deterministic, so ClearBad removes exactly what MarkBad
// wrote.
func (s *Store) badName(v string) string {
	salt := s.InstallSalt()
	if salt == "" {
		return safe(v)
	}
	mac := hmac.New(sha256.New, []byte(salt))
	mac.Write([]byte("bad|" + v))
	return hex.EncodeToString(mac.Sum(nil))[:24]
}

func (s *Store) versionPath() string { return filepath.Join(s.Dir, VersionFile) }
func (s *Store) goodPath() string    { return filepath.Join(s.Dir, "good") }
func (s *Store) badDir() string      { return filepath.Join(s.Dir, "bad") }

// RosterPath is the absolute path of the masked mesh-label roster file under the
// daemon-home. FEATURE 26: the basename is salt-derived (de-patterned per install)
// when a salt is present, else the fixed legacy RosterFile. Every reader
// (roster_fs, recoverRoster, the ArgvFromEnv cold-start) reads this through the
// SAME method against a Store keyed by the daemon-home, so they always agree.
func (s *Store) RosterPath() string {
	if n := relocate.MarkerBasename(s.InstallSalt(), "roster"); n != "" {
		return filepath.Join(s.Dir, n)
	}
	return filepath.Join(s.Dir, RosterFile)
}

// PidFilePath is the absolute path of the platform child's liveness pidfile under
// the daemon-home. FEATURE 26: salt-derived basename (de-patterned) when a salt is
// present, else the fixed legacy PlatformPidFile. The writer (platformsvc.Start
// via the loop) and the `focusd status` reader both resolve it through this
// method against the daemon-home, so they agree without a shared literal.
func (s *Store) PidFilePath() string {
	if n := relocate.MarkerBasename(s.InstallSalt(), "pidfile"); n != "" {
		return filepath.Join(s.Dir, n)
	}
	return filepath.Join(s.Dir, PlatformPidFile)
}

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
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return "", err
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	salt := hex.EncodeToString(buf)
	// HF4 F1: ATOMIC claim. Both mesh roles (A and B) call this UNSYNCHRONIZED in
	// build() before the singleton lock is held, so the old check-then-act let A
	// launch the platform under saltA while B's later write overwrote disk with
	// saltB — every later derivation (BinPath / PlatformArgv0 / status pgrep) then
	// used saltB != the running child's argv, so a live platform silently reported
	// DOWN. O_CREATE|O_EXCL makes EXACTLY ONE caller create the file and persist
	// ITS salt; the losers get EEXIST and adopt the winner's. Born 0600 (the salt
	// seeds every disguise derivation and must not be world-readable) — no
	// separate chmod (F4: a chmod that failed left the secret 0644 with no log).
	f, err := os.OpenFile(s.saltPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			// Lost the race: discard our value and adopt the winner's. The winner
			// creates the file (briefly empty) then writes; retry the read to
			// bridge that sub-millisecond window so we never adopt an empty salt.
			for i := 0; i < 100; i++ {
				if v := s.InstallSalt(); v != "" {
					return v, nil
				}
				time.Sleep(time.Millisecond)
			}
			return "", errors.New("ensure salt: peer claimed the salt but it did not materialize")
		}
		return "", fmt.Errorf("ensure salt: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write([]byte(salt)); err != nil {
		return "", fmt.Errorf("ensure salt: write: %w", err)
	}
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

// legacyLockFile is the fixed fallback basename of the per-workdir singleton
// lock (dev/test/legacy). The CROSS-GENERATION election uses a separate FIXED
// mode-keyed path (main.singletonLockPath) that CANNOT be salt-derived — two
// generations with different salts must flock the SAME file or twin platforms
// result — so only this per-workdir fallback is de-patterned here.
const legacyLockFile = "platform.lock"

// LockPath is the per-workdir singleton-lock file the winning daemon holds for
// the lifetime of its platform child. fd-tied advisory lock ⇒ kernel auto-
// releases on holder death, so a standby's next tick takes over. FEATURE 26:
// salt-derived basename (de-patterned, drops the 'platform' token) when a salt is
// present, else the fixed legacy basename.
func (s *Store) LockPath() string {
	if n := relocate.MarkerBasename(s.InstallSalt(), "lock"); n != "" {
		return filepath.Join(s.Dir, n)
	}
	return filepath.Join(s.Dir, legacyLockFile)
}

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

// Desired returns the configured desired version ("" if none). It un-masks a
// FEATURE-26 masked version.json and still accepts a legacy plaintext one.
func (s *Store) Desired() string {
	b, err := os.ReadFile(s.versionPath())
	if err != nil {
		return ""
	}
	data, _ := s.unmaskVer(b) // masked → payload; legacy/plaintext → raw bytes
	var c versionConfig
	if json.Unmarshal(data, &c) != nil {
		return ""
	}
	return c.Desired
}

// WriteDesired atomically records the desired version (masked when a salt exists).
func (s *Store) WriteDesired(v string) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(versionConfig{Desired: v})
	return atomicWrite(s.versionPath(), s.maskVer(b))
}

// Good / WriteGood track the last-known-good version (masked content, FEATURE 26).
func (s *Store) Good() string {
	b, err := os.ReadFile(s.goodPath())
	if err != nil {
		return ""
	}
	data, _ := s.unmaskVer(b)
	return strings.TrimSpace(string(data))
}

func (s *Store) WriteGood(v string) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	return atomicWrite(s.goodPath(), s.maskVer([]byte(v)))
}

// MarkBad records v as crash-looped (never run again). FEATURE 26: the filename
// is a keyed digest (no version leak) and the ORIGINAL version is the MASKED file
// CONTENT, so BadSet returns the exact version string and the executor's
// s.Bad[desired] lookup is symmetric (no silent miss for versions containing
// sanitised characters).
func (s *Store) MarkBad(v string) error {
	if err := os.MkdirAll(s.badDir(), 0o755); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.badDir(), s.badName(v)), s.maskVer([]byte(v)))
}

// ClearBad removes v's crash-looped marker, if present. Idempotent — a
// missing marker is not an error. This is the tamper-recovery primitive:
// once a genuine, signature-verified binary for v is back on disk, any prior
// "bad" verdict was about the TAMPERED bytes that have now been reverted, so
// v must become runnable again WITHOUT requiring a daemon process restart.
// FEATURE 26: removes both the keyed-digest name and the legacy path-sanitised
// name so a marker written before an upgrade is still cleared.
func (s *Store) ClearBad(v string) error {
	for _, name := range []string{s.badName(v), safe(v)} {
		if err := os.Remove(filepath.Join(s.badDir(), name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// BadSet returns the exact versions marked bad (read from the un-masked file
// CONTENT, so lookups by the original version string always match — regardless of
// the keyed-digest filename). Legacy plaintext markers are still accepted.
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
		data, _ := s.unmaskVer(b)
		if v := strings.TrimSpace(string(data)); v != "" {
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
