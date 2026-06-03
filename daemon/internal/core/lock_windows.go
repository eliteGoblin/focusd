//go:build windows

package core

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// FileLock is the Windows ProcessLock: a handle-tied LockFileEx advisory
// lock. The lock is bound to the open file handle, so the OS releases it when
// the holding process dies (crash-safe self-heal). The zero value is usable;
// NewFileLock is provided for explicit construction.
type FileLock struct {
	f *os.File
}

// NewFileLock returns an unlocked FileLock.
func NewFileLock() *FileLock { return &FileLock{} }

// TryAcquire opens (creating if needed) path and takes a non-blocking
// exclusive lock via LockFileEx with LOCKFILE_FAIL_IMMEDIATELY.
// ERROR_LOCK_VIOLATION => another live process holds it: ok=false, nil err.
func (l *FileLock) TryAcquire(path string) (bool, error) {
	if l.f != nil {
		return true, nil // already held by this instance — don't re-open/leak
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		// os.OpenFile returns *os.PathError, which embeds the full path
		// (= the disguised workdir). Return only the underlying error so the
		// disguised path can never escape via a wrapped/logged error.
		if pe, ok := err.(*os.PathError); ok {
			return false, fmt.Errorf("open lockfile: %w", pe.Err)
		}
		return false, errors.New("open lockfile failed")
	}
	var ol windows.Overlapped
	err = windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &ol,
	)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return false, nil // a live peer holds it — yield
		}
		return false, err
	}
	l.f = f
	return true, nil
}

// Release drops the lock and closes the handle. Idempotent: calling it
// without a held lock (or twice) is a no-op.
func (l *FileLock) Release() error {
	if l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	var ol windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
	return f.Close()
}
