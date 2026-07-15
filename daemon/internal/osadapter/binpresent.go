package osadapter

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// binPresentDeps are the injectable seams for ensureBinaryPresent so the
// decision + placement guards are unit-tested on Linux CI without launchd, a
// real self-delete, or the offline signing key. Production wiring lives in the
// darwin EnsureBinaryPresent.
type binPresentDeps struct {
	// selfExists reports whether the daemon binary FILE is present. It returns
	// (false, nil) for a clean ENOENT (the re-materialize trigger) and a non-nil
	// err only for an ambiguous stat failure (permission, I/O) — which aborts
	// rather than guessing the file is gone.
	selfExists func(path string) (bool, error)
	// readSelfBytes returns the retained-fd bytes of the ORIGINAL binary (still
	// readable after the path is unlinked). An error or empty result ⇒ no in-mesh
	// source; the out-of-band companion backstop still applies.
	readSelfBytes func() ([]byte, error)
	// verify reports whether the read bytes are a genuine, Ed25519-signed release
	// (program image ++ 64-byte trailer). Checked BEFORE anything is written —
	// refuse-then-place — so a corrupted retained image is never re-materialized.
	verify func(signedImage []byte) (bool, error)
	// randName is the fresh disguised basename generator (relocate.RandomBinaryName):
	// a new path each call also satisfies AMFI per-path CDHash rotation.
	randName func() string
	// place writes bytes to a NEW path atomically (temp+rename, 0755). CREATE-ONLY:
	// it never removes a directory.
	place func(bytes []byte, dst string) error
	// findAdoptable scans the IMMEDIATE files of workdir for an already-present,
	// signature-VERIFIED daemon binary at a path other than excludePath (the known-
	// missing SelfPath), returning its path or "" (issue #102-b belt). Only a signed
	// release in the 0700 daemon-home passes verification, so a partial/foreign file
	// is never adopted. Optional (nil ⇒ the belt is skipped; the create path runs).
	findAdoptable func(workdir, excludePath string) string
	// reinstall re-renders the three mesh plists at newSpec.SelfPath and
	// re-bootstraps them (installAll). Repointing SelfPath repoints all three by
	// construction.
	reinstall func(newSpec Spec) error
	// supportRoot is the containment root: the fresh binary path must sit directly
	// in spec.Workdir, which must itself be strictly under supportRoot.
	supportRoot string
}

// The refuse sentinels are surfaced (best-effort) so a heal that cannot proceed
// safely is logged, never silently placing an unverified or out-of-containment
// binary.
var (
	errNoSelfSource     = errors.New("osadapter: no retained-fd bytes to re-materialize the daemon binary")
	errUnverifiedSelf   = errors.New("osadapter: refusing to place a daemon binary that failed signature verification")
	errUnsafeCreatePath = errors.New("osadapter: refusing to place a daemon binary outside the contained workdir")
)

// ensureBinaryPresent is the seam-injected core of the in-mesh binary
// re-materialize fast path (FEATURE 22 follow-up). If our own binary FILE was
// deleted while this worker survives (`rm` = permanent-kill, since launchd can no
// longer exec the mesh), it verifies the retained-fd bytes and re-places them at
// a FRESH disguised path under the workdir, then repoints the three mesh plists —
// all within one reconcile tick, so the heartbeat keeps advancing and the ~30s
// companion staleness never trips. It is the FAST first line (partial kill:
// binary rm'd while A/B alive); the companion remains the LAST line (total kill /
// reboot).
//
// It is strictly CREATE-ONLY: it deletes NOTHING (no os.RemoveAll anywhere), so
// it can never degrade into the RemoveAll-class blast bug. The old (already
// ENOENT) path is left alone.
//
// Returns (newSelfPath, changed=true, err) when it (attempted to) re-materialize,
// and ("", false, nil) for every no-op gate (test mode, not the lock holder,
// binary still present). A non-nil err with changed=false is a refusal BEFORE any
// write; changed=true with a non-nil err means the binary WAS placed but the
// plist re-bootstrap failed (still progress — the caller adopts newSelfPath and
// the next tick's EnsureAll retries the launchd side).
func ensureBinaryPresent(d binPresentDeps, spec Spec, holdsLock bool) (newSelfPath string, changed bool, err error) {
	if spec.Mode == mode.Test { // test mode: never touch a real launchd mesh
		return "", false, nil
	}
	if !holdsLock { // only the single platform-lock holder re-materializes
		return "", false, nil
	}
	present, serr := d.selfExists(spec.SelfPath) // one cheap stat in steady state
	if serr != nil {
		return "", false, serr // ambiguous stat failure — do NOT assume deleted
	}
	if present { // steady state: the file is there, nothing to do
		return "", false, nil
	}

	// BELT (issue #102-b): before placing a fresh binary, check whether a sibling
	// already re-materialized a signature-VERIFIED daemon binary in the workdir
	// (at a path != the missing SelfPath). If so, ADOPT it and just repoint the
	// plists — never place a SECOND binary. 102-a's single-actor reinstall already
	// prevents the double-place, but this makes placement idempotent regardless of
	// lock timing. Only a signed release in the 0700 home verifies, so a partial
	// (.tmp) or foreign file is never adopted.
	if d.findAdoptable != nil {
		if adopt := d.findAdoptable(spec.Workdir, spec.SelfPath); adopt != "" {
			newSpec := spec
			newSpec.SelfPath = adopt
			if ierr := d.reinstall(newSpec); ierr != nil {
				return adopt, true, ierr
			}
			return adopt, true, nil
		}
	}

	// The binary FILE is gone. Re-materialize it from the retained-fd bytes.
	raw, rerr := d.readSelfBytes()
	if rerr != nil {
		return "", false, rerr
	}
	if len(raw) == 0 {
		return "", false, errNoSelfSource
	}
	ok, verr := d.verify(raw) // VERIFY BEFORE PLACE
	if verr != nil {
		return "", false, verr
	}
	if !ok {
		return "", false, errUnverifiedSelf
	}

	newPath := filepath.Join(spec.Workdir, d.randName()) // fresh disguised path
	if !safeToCreateUnder(newPath, spec.Workdir, d.supportRoot) {
		return "", false, errUnsafeCreatePath
	}
	if perr := d.place(raw, newPath); perr != nil { // atomic create-only, 0755
		return "", false, perr
	}

	// Repoint the three mesh plists at the fresh binary and re-bootstrap them.
	newSpec := spec
	newSpec.SelfPath = newPath
	if ierr := d.reinstall(newSpec); ierr != nil {
		// The binary IS placed; report the new path so the caller adopts it even
		// though the launchd rebuild failed (the next tick's EnsureAll retries).
		return newPath, true, ierr
	}
	return newPath, true, nil
}

// safeToCreateUnder reports whether newPath is a safe CREATE target for the
// re-materialized binary: an absolute path sitting DIRECTLY in workdir, where
// workdir is itself strictly nested under supportRoot. It is the create-side
// mirror of safeToRemoveWorkdir — belt-and-suspenders against a corrupted
// spec.Workdir ever widening where we write. newPath does not exist yet, so we
// resolve its PARENT (== workdir, which does exist) instead of the file itself.
// Pure → unit-tested on Linux CI.
func safeToCreateUnder(newPath, workdir, supportRoot string) bool {
	if newPath == "" || workdir == "" || supportRoot == "" {
		return false
	}
	if !filepath.IsAbs(newPath) || !filepath.IsAbs(workdir) || !filepath.IsAbs(supportRoot) {
		return false
	}
	// The base must be a real single path element — never a traversal that could
	// climb out of the workdir.
	base := filepath.Base(newPath)
	if base == "." || base == ".." || strings.ContainsRune(base, filepath.Separator) {
		return false
	}
	// workdir must be strictly under supportRoot. Resolve symlinks on both (they
	// exist) so this guard and the real write agree on the true paths — a
	// symlinked intermediate must not let the write escape supportRoot.
	rwd, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		return false
	}
	rroot, err := filepath.EvalSymlinks(supportRoot)
	if err != nil {
		return false
	}
	wd := filepath.Clean(rwd)
	root := filepath.Clean(rroot)
	rel, err := filepath.Rel(root, wd)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	// newPath's PARENT must resolve to exactly workdir (no symlinked escape via a
	// component of the parent).
	rparent, err := filepath.EvalSymlinks(filepath.Dir(newPath))
	if err != nil {
		return false
	}
	return filepath.Clean(rparent) == wd
}
