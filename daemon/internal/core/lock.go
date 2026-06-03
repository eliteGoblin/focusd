package core

// ProcessLock is a crash-safe, OS-advisory singleton lock tied to the
// holding process. The kernel releases it automatically if the holder dies.
type ProcessLock interface {
	// TryAcquire is non-blocking: ok=false (nil err) => another live process
	// holds it (yield). A non-nil err is a real I/O failure.
	TryAcquire(path string) (ok bool, err error)
	Release() error // idempotent
}
