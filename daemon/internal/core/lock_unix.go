//go:build unix

package core

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// FileLock is the unix ProcessLock: an fd-tied flock(2) advisory lock. The
// lock lives on an open file descriptor, so the kernel releases it
// automatically when the holding process dies (crash-safe self-heal). The
// zero value is usable; NewFileLock is provided for explicit construction.
type FileLock struct {
	f *os.File
}

// NewFileLock returns an unlocked FileLock.
func NewFileLock() *FileLock { return &FileLock{} }

// TryAcquire opens (creating if needed) path and takes a non-blocking
// exclusive flock. EWOULDBLOCK => another live process holds it: ok=false,
// nil err. A held lock is kept on the open fd for the process's lifetime.
func (l *FileLock) TryAcquire(path string) (bool, error) {
	if l.f != nil {
		return true, nil // already held by this instance — don't re-open/leak
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		// os.OpenFile returns *os.PathError, which embeds the full path
		// (= the disguised workdir). Return only the underlying errno so the
		// disguised path can never escape via a wrapped/logged error.
		if pe, ok := err.(*os.PathError); ok {
			return false, fmt.Errorf("open lockfile: %w", pe.Err)
		}
		return false, errors.New("open lockfile failed")
	}
	// EWOULDBLOCK == EAGAIN on Darwin and Linux; flock(2) names EWOULDBLOCK.
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return false, nil // a live peer holds it — yield
		}
		return false, err // syscall.Errno — carries no path
	}
	l.f = f
	return true, nil
}

// Release drops the lock and closes the fd. Idempotent: calling it without a
// held lock (or twice) is a no-op.
func (l *FileLock) Release() error {
	if l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return f.Close()
}
