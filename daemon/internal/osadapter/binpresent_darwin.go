//go:build darwin

package osadapter

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/relocate"
	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

// EnsureBinaryPresent is the darwin entry point for the in-mesh binary
// re-materialize fast path (FEATURE 22 follow-up). It wires the real seams
// (fstat-based read of the retained fd, Ed25519 verify, atomic create-only
// placer, installAll re-bootstrap) into the pure ensureBinaryPresent core and
// returns its result unchanged.
//
// retained is a read-only fd to the daemon's OWN binary, held open for the
// process lifetime (loop() opens it once). It may be nil (the retain open failed
// at startup); then a re-materialize cannot proceed in-mesh and the companion
// remains the backstop. holdsLock is the caller's platform-singleton-lock state
// (Executor.HoldsPlatformLock) — only the lock holder re-materializes, so mesh
// roles A and B never both place.
//
// selfRole is the CALLER's own mesh role (issue #102): the reinstall repoints the
// mesh via reinstallExceptSelf, which reloads every label EXCEPT the caller's own
// (booting self out last, no bootstrap) — so no process ever bootstraps its own
// executing label mid-install and SIGTERMs itself (the 102-a fault). The next
// reconcile's EnsureAll re-bootstraps self onto the new binary. The self label is
// derived from selfRole against the (roster-stable) spec.
func EnsureBinaryPresent(spec Spec, selfRole Role, holdsLock bool, retained *os.File) (newSelf string, changed bool, err error) {
	// Cheap no-op gates hoisted from the core so a non-participant skips the home
	// resolution + fd work below (and never logs a spurious home error every tick).
	// The core re-checks both, so its unit-tested guards remain authoritative.
	if spec.Mode == mode.Test || !holdsLock {
		return "", false, nil
	}
	home, herr := os.UserHomeDir()
	// System mode's SupportRoot is a fixed absolute path (home-independent); every
	// other mode roots the containment under home. If home can't be resolved for a
	// home-dependent mode, supportRoot would be RELATIVE and safeToCreateUnder
	// would refuse with a misleading errUnsafeCreatePath — surface the real cause
	// instead. Best-effort: the out-of-band companion remains the backstop.
	if herr != nil && spec.Mode != mode.System {
		return "", false, fmt.Errorf("osadapter: re-materialize: resolve home: %w", herr)
	}
	d := binPresentDeps{
		selfExists:    fileExists,
		readSelfBytes: func() ([]byte, error) { return readAllFromFD(retained) },
		verify:        verifySignedBytes,
		randName:      relocate.RandomBinaryName,
		place:         binPlacerFS{}.place,
		findAdoptable: findAdoptableBinary,
		reinstall: func(ns Spec) error {
			var rs rosterIO
			if ns.Workdir != "" {
				rs = newWorkdirRoster(ns.Workdir)
			}
			// Repoint via reinstallExceptSelf: reload every label EXCEPT the
			// caller's own, boot self out last (no self-bootstrap) — the next
			// EnsureAll re-bootstraps self onto the new binary. The self label is
			// stable across the path rotation (the roster is unchanged), so
			// ns.Label(selfRole) == spec.Label(selfRole).
			return reinstallExceptSelf(ns, launchctlCtl{m: ns.Mode}, laFS{m: ns.Mode}, rs, ns.Label(selfRole), time.Sleep)
		},
		supportRoot: mode.SupportRoot(spec.Mode, home),
	}
	return ensureBinaryPresent(d, spec, holdsLock)
}

// findAdoptableBinary scans the IMMEDIATE children of workdir for a regular file
// (other than excludePath) whose bytes pass Ed25519 verification — an already-
// placed, signed daemon binary a sibling re-materialized (issue #102-b). Returns
// its path or "". A partial `.tmp` from an in-flight place, or any foreign file,
// fails verification and is never adopted; only a signed release verifies.
func findAdoptableBinary(workdir, excludePath string) string {
	if workdir == "" {
		return ""
	}
	entries, err := os.ReadDir(workdir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(workdir, e.Name())
		if p == excludePath {
			continue
		}
		if ok, verr := sig.VerifyFile(p); verr == nil && ok {
			return p
		}
	}
	return ""
}

// verifySignedBytes splits a signed-release image (program ++ 64-byte trailer)
// and verifies it against the embedded Ed25519 key — the same trust root the
// daemon uses for peer binaries on disk. It refuses (ok=false) a truncated or
// unsigned image rather than trusting the bytes.
func verifySignedBytes(b []byte) (bool, error) {
	program, signature, serr := sig.SplitTrailer(b)
	if serr != nil {
		return false, serr
	}
	return sig.Verify(program, signature)
}

// fileExists reports whether path is present. A clean ENOENT ⇒ (false, nil); any
// other stat error is surfaced so an ambiguous failure never masquerades as "the
// binary was deleted" (which would trigger an unwarranted re-materialize).
func fileExists(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// readAllFromFD reads the entire file behind an open fd via pread (ReadAt through
// io.SectionReader), which works even after the path has been unlinked — the
// inode stays alive while the fd is open, so the original release bytes remain
// readable. It uses fstat (f.Stat) for the size bound, which also works on an
// unlinked inode. Reading through a size-bounded SectionReader with io.ReadAll
// avoids a manual make([]byte, int64) allocation. Returns an error for a nil fd
// (the retain open failed at startup).
func readAllFromFD(f *os.File) ([]byte, error) {
	if f == nil {
		return nil, errors.New("osadapter: no retained daemon-binary fd")
	}
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return io.ReadAll(io.NewSectionReader(f, 0, fi.Size()))
}
