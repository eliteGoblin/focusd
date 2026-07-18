package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/companion"
	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// companionDir builds a real companion.Dir rooted under a temp HOME and creates
// the folder so tests can drop heartbeat/desired/backup files into it.
func companionTestDir(t *testing.T) companion.Dir {
	t.Helper()
	home := t.TempDir()
	dir := companion.For(mode.User, home)
	if err := os.MkdirAll(dir.Root(), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// recorder captures the seam calls so a test asserts exactly what recover did.
type recorder struct {
	verified    []string
	execBin     string
	execDesired string
	execCalls   int
}

const goodDesired = "v0.16.3"

// TestRecoverWorksWithoutHOME is the #101 regression guard: the companion's
// recovery pass must run with the Dir derived from its OWN binary path and NO
// $HOME set (a system LaunchDaemon has none). It proves recover never depends on
// os.UserHomeDir()/mode — the DOA-on-system-launchd bug that killed the sole
// out-of-band rail. It also asserts the RanMarker is stamped on a completed pass
// (the firing signal status reads), on BOTH the no-op and the restore path.
func TestRecoverWorksWithoutHOME(t *testing.T) {
	// Unset HOME for the duration; restore after (os.Unsetenv is a raw global
	// mutation, but these tests are non-parallel).
	oldHome, had := os.LookupEnv("HOME")
	os.Unsetenv("HOME")
	t.Cleanup(func() {
		if had {
			os.Setenv("HOME", oldHome)
		}
	})

	// Dir derived purely from a binary path (HOME-free), rooted at a temp dir.
	root := t.TempDir()
	dir := companion.DirFromBinary(filepath.Join(root, "companion-bin"))
	if dir.Root() != root {
		t.Fatalf("DirFromBinary root = %q, want %q", dir.Root(), root)
	}

	t.Run("fresh heartbeat no-op still stamps RanMarker", func(t *testing.T) {
		writeFile(t, dir.Heartbeat(), "") // just touched (mtime = now) ⇒ fresh
		rec := &recorder{}
		err := recover(dir, time.Now(),
			func(p string) (bool, error) { rec.verified = append(rec.verified, p); return true, nil },
			func(bin, desired string) error { rec.execCalls++; return nil },
		)
		if err != nil {
			t.Fatalf("recover without HOME = %v, want nil", err)
		}
		if rec.execCalls != 0 {
			t.Fatalf("fresh heartbeat must be a no-op, execCalls=%d", rec.execCalls)
		}
		if _, statErr := os.Stat(dir.RanMarker()); statErr != nil {
			t.Fatalf("RanMarker not stamped on a completed no-op pass: %v", statErr)
		}
	})

	t.Run("stale heartbeat restore stamps RanMarker", func(t *testing.T) {
		os.Remove(dir.RanMarker()) // clear the marker from the previous sub-case
		staleMtime := time.Now().Add(-companion.StaleThreshold - time.Minute)
		writeFile(t, dir.Heartbeat(), "")
		if err := os.Chtimes(dir.Heartbeat(), staleMtime, staleMtime); err != nil {
			t.Fatal(err)
		}
		writeFile(t, dir.Desired(), goodDesired)
		writeFile(t, dir.Backup(), "SIGNED-DAEMON")
		rec := &recorder{}
		err := recover(dir, time.Now(),
			func(p string) (bool, error) { rec.verified = append(rec.verified, p); return true, nil },
			func(bin, desired string) error { rec.execCalls++; return nil },
		)
		if err != nil {
			t.Fatalf("recover (stale, no HOME) = %v, want nil", err)
		}
		if rec.execCalls != 1 {
			t.Fatalf("stale heartbeat must restore, execCalls=%d", rec.execCalls)
		}
		if _, statErr := os.Stat(dir.RanMarker()); statErr != nil {
			t.Fatalf("RanMarker not stamped on a completed restore pass: %v", statErr)
		}
	})
}

// TestRecoverFreshHeartbeatNoOp: a fresh heartbeat means the daemon is alive —
// recover must be a pure no-op: it never verifies the backup and never execs.
func TestRecoverFreshHeartbeatNoOp(t *testing.T) {
	dir := companionTestDir(t)
	writeFile(t, dir.Heartbeat(), "")        // just touched (mtime = now)
	writeFile(t, dir.Desired(), goodDesired) // present but must NOT be consulted
	writeFile(t, dir.Backup(), "SIGNED-DAEMON")

	rec := &recorder{}
	err := recover(dir, time.Now(),
		func(p string) (bool, error) { rec.verified = append(rec.verified, p); return true, nil },
		func(bin, desired string) error { rec.execCalls++; return nil },
	)
	if err != nil {
		t.Fatalf("recover (fresh) = %v, want nil", err)
	}
	if len(rec.verified) != 0 {
		t.Fatalf("verify ran on a fresh heartbeat; want 0, got %v", rec.verified)
	}
	if rec.execCalls != 0 {
		t.Fatalf("execDaemon ran on a fresh heartbeat; want 0, got %d", rec.execCalls)
	}
}

// TestRecoverStaleValidBackupExecs: a stale heartbeat + valid desired + a
// signature-verifying backup → recover promotes the backup and hands off to the
// idempotent watchdog (execDaemon) with the promote path + pinned version.
//
// ANTI-FIGHT (invariant #3): the handoff target is the daemon's `watchdog`,
// which no-ops a complete mesh (see runWatchdog tests in cmd/daemon) — so even
// this stale-but-actually-healthy path creates ZERO new generations.
func TestRecoverStaleValidBackupExecs(t *testing.T) {
	dir := companionTestDir(t)
	writeFile(t, dir.Heartbeat(), "")
	staleMtime := time.Now().Add(-companion.StaleThreshold - time.Minute)
	if err := os.Chtimes(dir.Heartbeat(), staleMtime, staleMtime); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir.Desired(), goodDesired)
	writeFile(t, dir.Backup(), "SIGNED-DAEMON-BYTES")

	rec := &recorder{}
	err := recover(dir, time.Now(),
		func(p string) (bool, error) { rec.verified = append(rec.verified, p); return true, nil },
		func(bin, desired string) error {
			rec.execCalls++
			rec.execBin = bin
			rec.execDesired = desired
			return nil
		},
	)
	if err != nil {
		t.Fatalf("recover (stale+valid) = %v, want nil", err)
	}
	if len(rec.verified) != 1 || rec.verified[0] != dir.Backup() {
		t.Fatalf("verify calls = %v, want [%s]", rec.verified, dir.Backup())
	}
	if rec.execCalls != 1 {
		t.Fatalf("execDaemon calls = %d, want 1", rec.execCalls)
	}
	if rec.execBin != dir.Promote() {
		t.Fatalf("execDaemon bin = %q, want promote path %q", rec.execBin, dir.Promote())
	}
	if rec.execDesired != goodDesired {
		t.Fatalf("execDaemon desired = %q, want %q", rec.execDesired, goodDesired)
	}
	// The verified backup was atomically placed at the promote path.
	if _, statErr := os.Stat(dir.Promote()); statErr != nil {
		t.Fatalf("promote path not placed: %v", statErr)
	}
}

// TestRecoverStaleBadBackupNoPromote: a stale heartbeat but a backup that FAILS
// signature verification → recover must NOT promote and must NOT exec; it
// returns an error. Closes the poisoned-offline-restore hole (acceptance #4).
func TestRecoverStaleBadBackupNoPromote(t *testing.T) {
	dir := companionTestDir(t)
	staleMtime := time.Now().Add(-companion.StaleThreshold - time.Minute)
	writeFile(t, dir.Heartbeat(), "")
	if err := os.Chtimes(dir.Heartbeat(), staleMtime, staleMtime); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir.Desired(), goodDesired)
	writeFile(t, dir.Backup(), "TAMPERED")

	rec := &recorder{}
	err := recover(dir, time.Now(),
		func(p string) (bool, error) { rec.verified = append(rec.verified, p); return false, nil }, // reject
		func(bin, desired string) error { rec.execCalls++; return nil },
	)
	if err == nil {
		t.Fatalf("recover (stale+bad backup) = nil, want error")
	}
	if rec.execCalls != 0 {
		t.Fatalf("execDaemon ran with an unverified backup; want 0, got %d", rec.execCalls)
	}
	if _, statErr := os.Stat(dir.Promote()); statErr == nil {
		t.Fatalf("backup was promoted despite failing verification")
	}
}

// TestRecoverStaleInvalidDesiredNoVerify: a stale heartbeat with a missing/
// garbage desired version is refused BEFORE the backup is even verified — the
// watchdog can't rebuild without a valid -v, so we never promote.
func TestRecoverStaleInvalidDesiredNoVerify(t *testing.T) {
	for _, desired := range []string{"", "latest", "garbage"} {
		t.Run("desired="+desired, func(t *testing.T) {
			dir := companionTestDir(t)
			staleMtime := time.Now().Add(-companion.StaleThreshold - time.Minute)
			writeFile(t, dir.Heartbeat(), "")
			if err := os.Chtimes(dir.Heartbeat(), staleMtime, staleMtime); err != nil {
				t.Fatal(err)
			}
			if desired != "" {
				writeFile(t, dir.Desired(), desired)
			}
			writeFile(t, dir.Backup(), "SIGNED-DAEMON")

			rec := &recorder{}
			err := recover(dir, time.Now(),
				func(p string) (bool, error) { rec.verified = append(rec.verified, p); return true, nil },
				func(bin, desired string) error { rec.execCalls++; return nil },
			)
			if err == nil {
				t.Fatalf("recover with invalid desired %q = nil, want error", desired)
			}
			if len(rec.verified) != 0 {
				t.Fatalf("verify ran before rejecting invalid desired %q: %v", desired, rec.verified)
			}
			if rec.execCalls != 0 {
				t.Fatalf("execDaemon ran with invalid desired %q", desired)
			}
		})
	}
}

// TestRecoverStampsRanMarkerBeforeHandoff (#106-b1): the RanMarker must be stamped
// at the START of a pass — BEFORE the (blocking) watchdog handoff — not only after
// it returns. Otherwise a legitimately long rebuild leaves the marker frozen and
// status reads the rail as not-firing. A fake execDaemon that BLOCKS proves the
// marker already exists while the handoff is in flight (which can only be the
// start-touch, since the end-touch runs after execDaemon returns).
func TestRecoverStampsRanMarkerBeforeHandoff(t *testing.T) {
	dir := companionTestDir(t)
	staleMtime := time.Now().Add(-companion.StaleThreshold - time.Minute)
	writeFile(t, dir.Heartbeat(), "")
	if err := os.Chtimes(dir.Heartbeat(), staleMtime, staleMtime); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir.Desired(), goodDesired)
	writeFile(t, dir.Backup(), "SIGNED-DAEMON")

	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- recover(dir, time.Now(),
			func(string) (bool, error) { return true, nil },
			func(bin, desired string) error {
				close(entered) // the blocking handoff has begun
				<-release      // ...and stays in flight until the test releases it
				return nil
			},
		)
	}()

	<-entered
	// The handoff is BLOCKED; the RanMarker must ALREADY be stamped (start-touch).
	_, statErr := os.Stat(dir.RanMarker())
	close(release)
	if statErr != nil {
		t.Fatalf("RanMarker not stamped before the blocking handoff (b1 regression): %v", statErr)
	}
	if err := <-done; err != nil {
		t.Fatalf("recover = %v, want nil", err)
	}
}

// TestRecoverMissingHeartbeatTreatedStale: with NO heartbeat file at all (a
// freshly-wiped state), recover treats the daemon as down and restores.
func TestRecoverMissingHeartbeatTreatedStale(t *testing.T) {
	dir := companionTestDir(t)
	// No heartbeat file.
	writeFile(t, dir.Desired(), goodDesired)
	writeFile(t, dir.Backup(), "SIGNED-DAEMON")

	rec := &recorder{}
	err := recover(dir, time.Now(),
		func(p string) (bool, error) { rec.verified = append(rec.verified, p); return true, nil },
		func(bin, desired string) error { rec.execCalls++; return nil },
	)
	if err != nil {
		t.Fatalf("recover (missing heartbeat) = %v, want nil", err)
	}
	if rec.execCalls != 1 {
		t.Fatalf("execDaemon calls = %d, want 1 (missing heartbeat ⇒ stale)", rec.execCalls)
	}
}
