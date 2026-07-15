//go:build darwin

package osadapter

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

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
func EnsureBinaryPresent(spec Spec, holdsLock bool, retained *os.File) (newSelf string, changed bool, err error) {
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
		reinstall: func(ns Spec) error {
			var rs rosterIO
			if ns.Workdir != "" {
				rs = newWorkdirRoster(ns.Workdir)
			}
			return installAll(ns, launchctlCtl{m: ns.Mode}, laFS{m: ns.Mode}, rs)
		},
		supportRoot: mode.SupportRoot(spec.Mode, home),
	}
	return ensureBinaryPresent(d, spec, holdsLock)
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
