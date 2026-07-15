package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/companion"
)

// recover is the companion's single recovery pass (FEATURE 18 / ADR-0020),
// injectable for unit testing. verify is the signature check (sig.VerifyFile in
// production); execDaemon runs the promoted daemon binary's idempotent
// `watchdog` subcommand.
//
// Steps:
//  1. Heartbeat fresh (< StaleThreshold) → the daemon is alive → no-op.
//  2. Stale (or heartbeat missing) → read the pinned desired version (reject if
//     not valid semver), signature-verify the offline backup; an invalid/
//     tampered backup is REFUSED WITHOUT promoting (path-free error).
//  3. Atomically place the verified backup at the promote path (0755), then
//     hand off to `daemon watchdog -v <desired>`.
//
// ANTI-FIGHT (critical): restoration goes through the daemon's IDEMPOTENT
// watchdog, which no-ops when the mesh is already complete
// (FindCurrentInstall/meshComplete). The heartbeat is ONLY an optimization — the
// daemon's meshComplete is the authority — so a false "stale" costs exactly one
// harmless watchdog run and creates ZERO new generations on a healthy install.
func recover(
	dir companion.Dir, now time.Time,
	verify func(path string) (bool, error),
	execDaemon func(bin, desired string) error,
) error {
	// 1. Heartbeat fresh → daemon alive → no-op. A missing heartbeat (Stat
	//    error) falls through to the restore path (treated as stale).
	if fi, err := os.Stat(dir.Heartbeat()); err == nil {
		if !companion.DecideStale(fi.ModTime(), now) {
			touchRan(dir, now) // completed pass (no-op path): record the rail is firing
			return nil
		}
	}

	// 2. Stale (or missing): restore. The watchdog needs an explicit -v and
	//    refuses garbage, so validate the pinned version up front.
	desired, derr := readTrimmed(dir.Desired())
	if derr != nil || !companion.IsValidVersion(desired) {
		return fmt.Errorf("companion: missing/invalid desired version; refusing to restore")
	}
	// Signature-verify the offline backup BEFORE it can be promoted + exec'd as
	// root. A genuine backup is a signed daemon binary and verifies; a tampered/
	// poisoned copy fails → refuse WITHOUT promoting. PATH-FREE message.
	if ok, verr := verify(dir.Backup()); verr != nil || !ok {
		return fmt.Errorf("companion: offline backup failed signature verification; refusing to promote")
	}

	// 3. Atomically place the verified backup, then hand off to the idempotent
	//    watchdog rebuild.
	if err := placeExecutable(dir.Backup(), dir.Promote()); err != nil {
		return fmt.Errorf("companion: place backup failed")
	}
	if err := execDaemon(dir.Promote(), desired); err != nil {
		return err
	}
	touchRan(dir, now) // completed pass (restore path): record the rail is firing
	return nil
}

// touchRan sets the RanMarker's mtime to now (best-effort), recording that this
// recovery pass COMPLETED. status treats the rail as firing only when this marker
// is recent — so a companion that dies before reaching a completion point (the
// #101 no-$HOME class exited before recover ran at all) never touches it and the
// rail honestly reads as not-firing. Only the mtime matters; content is empty.
func touchRan(dir companion.Dir, now time.Time) {
	p := dir.RanMarker()
	if err := os.Chtimes(p, now, now); err != nil && os.IsNotExist(err) {
		// Create it, then stamp the intended mtime (a fresh create's mtime is the
		// real wall clock; align it to now for deterministic staleness checks).
		if f, cerr := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644); cerr == nil {
			_ = f.Close()
			_ = os.Chtimes(p, now, now)
		}
	}
}

// readTrimmed reads a small file and trims surrounding whitespace.
func readTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// placeExecutable copies src to dst atomically (temp + rename, same dir → same
// filesystem) with 0755, so a crash mid-copy can't leave a half-written binary
// that exec would run.
func placeExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
