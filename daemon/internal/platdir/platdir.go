// Package platdir owns the SEPARATION between the daemon's own home
// (daemon-home: the daemon binary + daemon-owned state that must survive a
// platform wipe) and the platform's disposable working directory
// (platform-workdir: bin/<v>/platform, plugins, state.db, platform.log).
//
// FEATURE 21 (HF1): before this split, everything lived under ONE relocated
// workdir and the daemon binary lived INSIDE it — so `rm -rf <workdir>` also
// deleted the recoverer's own executable and the mesh plists pointed at a
// missing file (dead, no self-heal). Now the two roots have distinct
// lifetimes:
//
//   - daemon-home holds the binary + roster/version.json/good/bad. The mesh
//     plists point at the binary HERE. Deleting the platform-workdir cannot
//     touch it.
//   - platform-workdir is disposable. A small POINTER FILE in daemon-home
//     records its current path. Deleting the platform-workdir costs only the
//     platform, which the daemon re-fetches/re-extracts + restarts.
//
// The pointer is the ONLY link from daemon-home to the platform-workdir. When
// the pointer is missing, its target is gone, or the target fails the
// containment guard, [Resolve] RECREATES the platform-workdir at a FRESH path
// under the support root and rewrites the pointer — the self-heal, and the
// reason recovery relies on NOTHING left inside the deleted folder.
package platdir

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/eliteGoblin/focusd/daemon/internal/relocate"
)

// pointerBasename is the disguised basename of the pointer file inside
// daemon-home. Apple-metadata-looking so a casual `ls` in the (already hidden)
// daemon-home does not flag it. Not a real-install identifier — a generic
// disguise chosen for this feature (cf. the singleton-lock disguise).
const pointerBasename = ".com.apple.metadata.store.plist"

// sentinelBasename marks a directory as a platform-workdir this package
// created. It lets the orphan sweep tell a platform-workdir apart from a
// daemon-home (which has NO sentinel) without guessing from contents, so the
// sweep can never delete a daemon-home by mistake.
const sentinelBasename = ".com.apple.metadata.pwd.plist"

// PointerPath is the absolute path of the pointer file inside daemonHome.
func PointerPath(daemonHome string) string {
	return filepath.Join(daemonHome, pointerBasename)
}

// SentinelPath is the absolute path of the platform-workdir sentinel inside a
// platform-workdir.
func SentinelPath(platformWorkdir string) string {
	return filepath.Join(platformWorkdir, sentinelBasename)
}

// IsPlatformWorkdir reports whether dir carries the platform-workdir sentinel
// (i.e. this package created it). Used by the orphan sweep so it only ever
// deletes platform-workdirs, never a daemon-home.
func IsPlatformWorkdir(dir string) bool {
	fi, err := os.Stat(SentinelPath(dir))
	return err == nil && !fi.IsDir()
}

// Read returns the platform-workdir recorded in daemonHome's pointer file, or
// "" when the pointer is absent/unreadable/empty. It does NOT validate the
// target (see SafeTarget) or check that it exists (see Resolve).
func Read(daemonHome string) string {
	b, err := os.ReadFile(PointerPath(daemonHome))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Write atomically records target as daemonHome's platform-workdir.
func Write(daemonHome, target string) error {
	if err := os.MkdirAll(daemonHome, 0o700); err != nil {
		return err
	}
	p := PointerPath(daemonHome)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(target+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// SafeTarget is the containment guard for a pointer target — the same
// blast-radius discipline the generation sweep uses (osadapter.safeToRemoveWorkdir).
// A target is safe ONLY when ALL hold:
//   - target and supportRoot are non-empty absolute paths;
//   - target is STRICTLY nested under supportRoot (never the root itself,
//     never an escape) — so a corrupt/hostile pointer can't send the daemon
//     to write platform state at "/", a sibling tree, or outside the mode's
//     Application Support root; and
//   - target is NOT the daemon-home and NOT an ancestor of it — a pointer
//     that resolved to (an ancestor of) daemon-home would let a later
//     platform-workdir wipe take the daemon's own home down too, re-opening
//     exactly the shared-fate defect this feature closes.
//
// Pure + lexical (no EvalSymlinks / no stat) so it is unit-tested against
// relative, outside-root, and ancestor inputs that need not exist on disk.
func SafeTarget(target, supportRoot, daemonHome string) bool {
	if target == "" || supportRoot == "" ||
		!filepath.IsAbs(target) || !filepath.IsAbs(supportRoot) {
		return false
	}
	target = filepath.Clean(target)
	root := filepath.Clean(supportRoot)

	// Strictly under supportRoot: a valid relative path that is neither "."
	// (== root) nor an escape ("..").
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}

	if daemonHome != "" {
		home := filepath.Clean(daemonHome)
		if target == home {
			return false // never place platform state ON the daemon-home
		}
		// Reject target being an ancestor of daemon-home (a wipe of it would
		// take daemon-home down too).
		if r, rerr := filepath.Rel(target, home); rerr == nil &&
			r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

// Create makes a FRESH disguised platform-workdir under supportRoot, marks it
// with the sentinel, and returns its path. The name is drawn from the same
// disguise pool as the daemon-home so a platform-workdir is indistinguishable
// from any other hidden Application-Support dir.
func Create(supportRoot string) (string, error) {
	dir := relocate.HiddenWorkdir(supportRoot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Best-effort sentinel: a write failure only weakens the orphan sweep's
	// ability to recognise this dir later; it must not fail the create (the
	// platform can still run from an unmarked dir).
	_ = os.WriteFile(SentinelPath(dir), nil, 0o600)
	return dir, nil
}

// Resolve returns the platform-workdir for daemonHome, self-healing as needed.
// It reads the pointer; if the recorded target is present, passes the
// containment guard, and exists on disk, it is returned unchanged. Otherwise
// (pointer missing, target wiped, or target unsafe) a FRESH platform-workdir is
// created under supportRoot and the pointer is rewritten to it — this is the
// reliable re-establishment, and it relies on nothing left inside the deleted
// folder.
func Resolve(daemonHome, supportRoot string) (string, error) {
	if target := Read(daemonHome); target != "" &&
		SafeTarget(target, supportRoot, daemonHome) {
		if fi, err := os.Stat(target); err == nil && fi.IsDir() {
			return target, nil // healthy pointer → keep the existing platform-workdir
		}
	}
	fresh, err := Create(supportRoot)
	if err != nil {
		return "", err
	}
	if err := Write(daemonHome, fresh); err != nil {
		return "", err
	}
	return fresh, nil
}
