package core

import (
	"sync"
	"testing"
)

// TestEnsureInstallSaltConcurrentSingleWinner reproduces HF4 F1 and proves the
// fix. Both mesh roles (A and B) call EnsureInstallSalt UNSYNCHRONIZED in build()
// before the singleton lock is held. The old check-then-act let each generate +
// write its OWN salt (via a shared fixed temp), so results diverged and the last
// writer's salt clobbered disk — every later derivation (BinPath / PlatformArgv0
// / status pgrep) then used a salt != the running child's argv, so a live
// platform silently reported DOWN. The atomic O_EXCL claim must make ALL
// concurrent callers observe ONE identical salt (the winner's), none empty, and
// the persisted salt must equal what everyone observed.
func TestEnsureInstallSaltConcurrentSingleWinner(t *testing.T) {
	st := &Store{Dir: t.TempDir()}

	const n = 32
	results := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start // release all goroutines at once to maximize the race
			results[i], errs[i] = st.EnsureInstallSalt()
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: EnsureInstallSalt error: %v", i, err)
		}
	}
	first := results[0]
	if first == "" {
		t.Fatal("goroutine 0 observed an empty salt")
	}
	for i, got := range results {
		if got == "" {
			t.Fatalf("goroutine %d observed an EMPTY salt (F1: winner not materialized)", i)
		}
		if got != first {
			t.Fatalf("salt DIVERGENCE (F1): goroutine %d saw %q, goroutine 0 saw %q", i, got, first)
		}
	}
	// The persisted salt must match what every caller observed (no clobber).
	if got := st.InstallSalt(); got != first {
		t.Fatalf("persisted salt %q != observed %q", got, first)
	}
}

// TestEnsureInstallSaltIdempotent: a second call returns the SAME salt (stable
// for the install's lifetime) so every role/status derives identical paths.
func TestEnsureInstallSaltIdempotent(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	a, err := st.EnsureInstallSalt()
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.EnsureInstallSalt()
	if err != nil {
		t.Fatal(err)
	}
	if a == "" || a != b {
		t.Fatalf("EnsureInstallSalt not idempotent: %q then %q", a, b)
	}
}
