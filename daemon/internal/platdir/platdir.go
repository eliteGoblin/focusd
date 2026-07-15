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
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"

	"github.com/eliteGoblin/focusd/daemon/internal/relocate"
)

// legacyPointerBasename is the FIXED pointer basename used before FEATURE 26.
// New installs salt-derive the basename (relocate.MarkerBasename "pointer"); this
// literal is retained ONLY as a read fallback so an upgrade that seeds the salt
// after the legacy pointer was written still finds the live platform-workdir
// (migration: don't orphan the live install).
const legacyPointerBasename = ".com.apple.metadata.store.plist"

// installSalt reads the per-install disguise salt from daemonHome (the fixed
// relocate.SaltBasename literal), or "" when absent (dev/test/legacy → the
// callers fall back to the fixed legacy pointer basename + plaintext content).
func installSalt(daemonHome string) string {
	b, err := os.ReadFile(filepath.Join(daemonHome, relocate.SaltBasename))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// pointerBasename returns daemonHome's pointer basename: salt-derived when a salt
// is present (de-patterned per install), else the fixed legacy literal.
func pointerBasename(daemonHome string) string {
	if n := relocate.MarkerBasename(installSalt(daemonHome), "pointer"); n != "" {
		return n
	}
	return legacyPointerBasename
}

// PointerPath is the absolute path of the pointer file inside daemonHome (its
// current, possibly salt-derived, basename).
func PointerPath(daemonHome string) string {
	return filepath.Join(daemonHome, pointerBasename(daemonHome))
}

// pointerMaskKey is the deterministic XOR key for the pointer file CONTENT,
// distinct from every other salt-keyed mask. Empty salt ⇒ no mask (plaintext,
// legacy/test).
func pointerMaskKey(salt string) []byte {
	if salt == "" {
		return nil
	}
	h := sha256.Sum256([]byte(salt + "|pointer"))
	return h[:]
}

// xorMask returns src XOR key (its own inverse). key nil ⇒ src unchanged.
func xorMask(src, key []byte) []byte {
	if len(key) == 0 {
		return append([]byte(nil), src...)
	}
	out := make([]byte, len(src))
	for i, b := range src {
		out[i] = b ^ key[i%len(key)]
	}
	return out
}

// IsPlatformWorkdir reports whether dir is a platform-workdir this package
// created — recognised by the FORWARD content magic (a sentinel file whose bytes
// un-mask to pwdMagic) OR the LEGACY two-signal migration marker. Used by the
// generation sweeps so they only ever delete a platform-workdir, never a
// daemon-home or a real app folder. Content-only forward recognition works
// cross-generation (no salt needed) and cannot match a real app folder.
func IsPlatformWorkdir(dir string) bool {
	return hasMarker(dir, pwdMagic()) || isLegacyPlatformWorkdir(dir)
}

// IsDaemonHome reports whether dir is a daemon-home this package marked (a
// sentinel file whose bytes un-mask to dhMagic). The daemon-home orphan sweep
// gates its RemoveAll on this positive content match — never on a name or on a
// state.db heuristic — so a real app folder is never a delete candidate.
func IsDaemonHome(dir string) bool {
	return hasMarker(dir, dhMagic())
}

// MarkPlatformWorkdir writes the platform-workdir content sentinel into dir.
// Best-effort (a failure only weakens later recognition).
func MarkPlatformWorkdir(dir string) { writeSentinel(dir, pwdMagic()) }

// MarkDaemonHome writes the daemon-home content sentinel into dir. Called once at
// install so an orphaned daemon-home is later recognisable + sweepable by
// content. Best-effort.
func MarkDaemonHome(dir string) { writeSentinel(dir, dhMagic()) }

// Read returns the platform-workdir recorded in daemonHome's pointer file, or
// "" when the pointer is absent/unreadable/empty. It transparently un-masks a
// salt-masked pointer, accepts a legacy plaintext pointer (self-heals on the next
// Write), and falls back to the legacy pointer basename during an upgrade so the
// live platform-workdir is not orphaned. It does NOT validate the target (see
// SafeTarget) or check that it exists (see Resolve).
func Read(daemonHome string) string {
	salt := installSalt(daemonHome)
	raw, err := os.ReadFile(PointerPath(daemonHome))
	if err != nil {
		// Migration: a just-upgraded install may still have the pointer at the
		// legacy basename (the salt was seeded after it was written). Adopt it.
		if salt != "" {
			if lb, lerr := os.ReadFile(filepath.Join(daemonHome, legacyPointerBasename)); lerr == nil {
				raw = lb
			} else {
				return ""
			}
		} else {
			return ""
		}
	}
	// Prefer the un-masked interpretation; accept a legacy plaintext path if the
	// bytes are already a valid absolute path (an install written before the salt
	// existed). filepath.IsAbs is the validity oracle: a masked path is
	// overwhelmingly non-"/"-leading, so this disambiguates without a format byte.
	if unmasked := strings.TrimSpace(string(xorMask(raw, pointerMaskKey(salt)))); filepath.IsAbs(unmasked) {
		return unmasked
	}
	if plain := strings.TrimSpace(string(raw)); filepath.IsAbs(plain) {
		return plain // legacy plaintext → Resolve/Write will re-mask it
	}
	return ""
}

// Write atomically records target as daemonHome's platform-workdir, XOR-masking
// the content with the salt-derived key (plaintext when no salt: legacy/test).
func Write(daemonHome, target string) error {
	if err := os.MkdirAll(daemonHome, 0o700); err != nil {
		return err
	}
	p := PointerPath(daemonHome)
	payload := xorMask([]byte(target+"\n"), pointerMaskKey(installSalt(daemonHome)))
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
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
		// Reject the reverse nesting too: daemon-home being an ancestor of the
		// target (target strictly INSIDE daemon-home). A platform-workdir nested
		// under daemon-home re-couples their lifetimes — the very shared-fate
		// defect this feature closes — so the disposable engine storage must live
		// as a SIBLING under the support root, never a child of daemon-home.
		if r, rerr := filepath.Rel(home, target); rerr == nil &&
			r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

// Create EXCLUSIVELY makes a FRESH disguised platform-workdir under supportRoot,
// marks it with the content sentinel, and returns its path. The name is a
// shape-ensemble blend (relocate.FreshHiddenDir) that reads as an ordinary
// app-support entry — different from the daemon-home and from every other
// generation. FreshHiddenDir uses os.Mkdir (never MkdirAll), so it can NEVER
// adopt a pre-existing real app folder: our sentinel only ever lands in a
// directory we exclusively created. That exclusivity is the invariant that keeps
// the content-gated sweep from ever deleting a real folder.
func Create(supportRoot string) (string, error) {
	dir, err := relocate.FreshHiddenDir(supportRoot)
	if err != nil {
		return "", err
	}
	MarkPlatformWorkdir(dir)
	return dir, nil
}

// Resolve returns the platform-workdir for daemonHome, self-healing as needed.
// It reads the pointer; if the recorded target is present, passes the
// containment guard, and exists on disk, it is returned unchanged. Otherwise
// (pointer missing, target wiped, or target unsafe) a FRESH platform-workdir is
// created under supportRoot and the pointer is rewritten to it — this is the
// reliable re-establishment, and it relies on nothing left inside the deleted
// folder.
//
// The guard is TWO-LAYERED against a symlinked target. SafeTarget is a purely
// LEXICAL pre-check: it treats the pointer string as text and so cannot see a
// symlink whose text is under supportRoot but whose real target escapes (e.g.
// <root>/pwd -> /outside, or -> daemonHome). We therefore also EvalSymlinks the
// target and re-run containment on the RESOLVED path (target-vs-root and
// target-vs-daemonHome) — mirroring osadapter.safeToRemoveWorkdir, so a caller
// that later os.RemoveAll's this workdir cannot be sent outside the root. A
// target that fails either layer is discarded and a fresh workdir is created.
//
// ACCEPTED TOCTOU: the resolved-containment check races the caller's later use
// of the path — an attacker with write access to the pointer's parent could
// swap the symlink after this returns. In practice daemonHome is a hidden,
// 0700 dir owned by the daemon; an adversary with write access there already
// owns the install. The re-check closes the realistic accidental/relative-link
// case; the residual race is out of this feature's threat model.
func Resolve(daemonHome, supportRoot string) (string, error) {
	if target := Read(daemonHome); target != "" &&
		SafeTarget(target, supportRoot, daemonHome) {
		if fi, err := os.Stat(target); err == nil && fi.IsDir() &&
			resolvedTargetContained(target, supportRoot, daemonHome) {
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

// resolvedTargetContained re-runs SafeTarget's containment on the SYMLINK-
// RESOLVED target (and, when it exists, the resolved supportRoot / daemonHome)
// so a symlinked pointer target that is lexically-safe but really escapes the
// support root — or points at daemonHome — is rejected. It mirrors the
// EvalSymlinks discipline in osadapter.safeToRemoveWorkdir. EvalSymlinks failure
// (a target we cannot stat through) → not contained → discard the pointer.
func resolvedTargetContained(target, supportRoot, daemonHome string) bool {
	rtarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	// Resolve the root/home too so the comparison is real-path vs real-path.
	// Best-effort: an unresolvable root/home falls back to its cleaned form
	// (SafeTarget already proved the lexical relationship), so this only ever
	// ADDS the symlink-escape rejection, never loosens the earlier check.
	rroot := supportRoot
	if r, rerr := filepath.EvalSymlinks(supportRoot); rerr == nil {
		rroot = r
	}
	rhome := daemonHome
	if daemonHome != "" {
		if h, herr := filepath.EvalSymlinks(daemonHome); herr == nil {
			rhome = h
		}
	}
	return SafeTarget(rtarget, rroot, rhome)
}
