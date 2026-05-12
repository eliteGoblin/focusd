package infra

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Relocator copies (or hard-links) the appmon binary to a randomized,
// system-looking basename under an obfuscated cache directory. Daemons exec
// from the relocated path so `killall appmon` does not match the running
// process (killall matches on the kernel's p_comm, which is set from the
// exec'd file basename — not from argv[0]).
//
// Neither the directory nor the basename contains the literal "appmon", so
// `pkill -f appmon` also fails to match.
//
//	Dir:      ~/.cache/.com.apple.xpc.<host-hash>/
//	Basename: e.g. com.apple.cfprefsd.xpc.a8f3b2c1
type Relocator struct {
	dir string
}

// NewRelocator builds a Relocator rooted under the given home directory.
// Callers should pass the real user's home (via GetRealUserHome when running
// under sudo).
func NewRelocator(home string) *Relocator {
	return &Relocator{dir: relocatorDir(home)}
}

// Dir returns the cache directory where relocated binaries live.
func (r *Relocator) Dir() string { return r.dir }

// Relocate copies srcPath to <dir>/<random-name>. Hard link is attempted
// first (same inode, near-zero cost); on failure (cross-fs or unsupported)
// it falls back to an atomic copy. Returns the new path.
func (r *Relocator) Relocate(srcPath string) (string, error) {
	if err := os.MkdirAll(r.dir, 0700); err != nil {
		return "", fmt.Errorf("create relocator dir: %w", err)
	}
	dst := filepath.Join(r.dir, randomRelocatedName())

	if err := os.Link(srcPath, dst); err == nil {
		_ = os.Chmod(dst, 0755)
		return dst, nil
	}

	if err := relocateCopy(srcPath, dst); err != nil {
		return "", fmt.Errorf("relocate %s -> %s: %w", srcPath, dst, err)
	}
	return dst, nil
}

// CleanStale removes files in the relocator dir that are not in `keep` and
// whose mtime is older than minAge. The minAge guard avoids racing a child
// process that has been linked but not yet exec'd. Unlinking a running
// binary on macOS is safe — the kernel preserves the inode until the
// process exits — so aggressive sweeps do not crash live daemons.
func (r *Relocator) CleanStale(keep []string, minAge time.Duration) error {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	keepSet := make(map[string]struct{}, len(keep))
	for _, k := range keep {
		if k != "" {
			keepSet[k] = struct{}{}
		}
	}
	cutoff := time.Now().Add(-minAge)
	for _, e := range entries {
		path := filepath.Join(r.dir, e.Name())
		if _, kept := keepSet[path]; kept {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(path)
	}
	return nil
}

// FindProcessesUsingDir returns PIDs of running processes whose executable
// path lives under the relocator directory. The watcher uses this to find
// orphan daemons — anything in our cache dir whose PID isn't recorded in
// the encrypted registry is by definition not a daemon we own. This makes
// the registry the single source of truth for live daemon membership;
// rolling back, racing spawns, or stale state self-heal on the next sweep.
//
// Implementation: shells out to `ps -axww -o pid=,command=`. argv[0] is the
// full path passed to execve, which on macOS is the path used to start the
// process — so any of our re-exec'd daemons will report their relocated
// path here. Errors are returned to the caller; nil pids on success when
// nothing matches.
func (r *Relocator) FindProcessesUsingDir() ([]int, error) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	prefix := r.dir + string(os.PathSeparator)

	lines := strings.Split(string(out), "\n")
	pids := make([]int, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimLeft(line, " \t")
		if line == "" {
			continue
		}
		// "<pid> <command...>" — split on first whitespace.
		spaceIdx := strings.IndexAny(line, " \t")
		if spaceIdx < 0 {
			continue
		}
		pidStr := line[:spaceIdx]
		cmd := strings.TrimLeft(line[spaceIdx:], " \t")
		if !strings.HasPrefix(cmd, prefix) {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

// LiveDaemon describes a running appmon daemon process as observed via ps.
// Used by the CLI status command — ps is world-readable, so status works
// for non-root users even when the encrypted registry is owned by root.
type LiveDaemon struct {
	PID  int
	Role string // "watcher" | "guardian" | "" (unknown)
	Path string // absolute path to the executable
}

// DetectLiveDaemons returns every appmon daemon process visible in `ps`,
// regardless of which mode's registry recorded it. Matches:
//   - basename == "appmon" (legacy v0.5.0 daemons), OR
//   - absolute path under any user's relocator cache dir
//     (`~/.cache/.com.apple.xpc.<host-hash>/`)
//
// AND argv contains `daemon --role <role>` (so CLI processes like
// `appmon start` are not reported).
//
// Source of truth for "is appmon actually running" from the perspective
// of a process that cannot read the encrypted registry. The registry is
// authoritative for *bookkeeping* (which PIDs we own), but the kernel is
// authoritative for *liveness*.
func DetectLiveDaemons() ([]LiveDaemon, error) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return parseLiveDaemons(string(out)), nil
}

// parseLiveDaemons is the pure half of DetectLiveDaemons. Takes raw ps
// output, returns the matching daemon set — unit-testable without
// depending on real-system process state.
func parseLiveDaemons(psOutput string) []LiveDaemon {
	lines := strings.Split(psOutput, "\n")
	out := make([]LiveDaemon, 0, 4)
	for _, line := range lines {
		line = strings.TrimLeft(line, " \t")
		if line == "" {
			continue
		}
		spaceIdx := strings.IndexAny(line, " \t")
		if spaceIdx < 0 {
			continue
		}
		pidStr := line[:spaceIdx]
		cmd := strings.TrimLeft(line[spaceIdx:], " \t")

		execEnd := strings.IndexAny(cmd, " \t")
		if execEnd < 0 {
			continue
		}
		exe := cmd[:execEnd]
		base := filepath.Base(exe)

		isLegacy := base == "appmon"
		// Relocator basenames follow com.apple.<service>.<suffix>.<hex>.
		// We treat any process under "/.cache/.com.apple.xpc." anywhere
		// in the absolute path as a relocated daemon — hostname-hash
		// agnostic so this works across hosts in tests.
		isRelocated := strings.Contains(exe, "/.cache/.com.apple.xpc.")
		if !isLegacy && !isRelocated {
			continue
		}
		if !strings.Contains(cmd, " daemon ") || !strings.Contains(cmd, "--role") {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		out = append(out, LiveDaemon{
			PID:  pid,
			Role: extractRoleArg(cmd),
			Path: exe,
		})
	}
	return out
}

// extractRoleArg pulls the value following `--role` out of the command
// line. Returns "" if not present or malformed.
func extractRoleArg(cmd string) string {
	const marker = "--role "
	i := strings.Index(cmd, marker)
	if i < 0 {
		return ""
	}
	rest := cmd[i+len(marker):]
	end := strings.IndexAny(rest, " \t")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// FindLegacyAppmonDaemons returns PIDs of running processes whose basename
// is literally "appmon" AND whose argv contains "daemon --role". These are
// daemon processes from pre-relocation builds (v0.5.0 and earlier) that
// did not run through the Relocator — they survive a fresh install of a
// relocation-aware binary because they're not in the relocator cache dir
// and the watcher's cache-dir-based reaper misses them.
//
// The argv filter ("daemon --role") excludes short-lived CLI invocations
// like `appmon start` or `appmon status`, so callers can safely SIGKILL
// the returned PIDs without collateral damage to the user's terminal.
func FindLegacyAppmonDaemons() ([]int, error) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return parseLegacyAppmonDaemons(string(out)), nil
}

// parseLegacyAppmonDaemons is the pure-function half of
// FindLegacyAppmonDaemons — it takes the output of
// `ps -axww -o pid=,command=` and returns matching PIDs. Split out so
// unit tests can exercise the filter logic with fixture input rather
// than depending on real-system process state.
func parseLegacyAppmonDaemons(psOutput string) []int {
	lines := strings.Split(psOutput, "\n")
	pids := make([]int, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimLeft(line, " \t")
		if line == "" {
			continue
		}
		spaceIdx := strings.IndexAny(line, " \t")
		if spaceIdx < 0 {
			continue
		}
		pidStr := line[:spaceIdx]
		cmd := strings.TrimLeft(line[spaceIdx:], " \t")

		// Process executable must basename to "appmon" — i.e. command
		// starts with a path ending in "/appmon" followed by a space.
		// Excludes processes running from the relocator cache (handled
		// separately by Relocator.FindProcessesUsingDir).
		execEnd := strings.IndexAny(cmd, " \t")
		if execEnd < 0 {
			continue
		}
		exe := cmd[:execEnd]
		if filepath.Base(exe) != "appmon" {
			continue
		}

		// argv must contain `daemon --role` — distinguishes the long-
		// running watcher/guardian processes from any sibling CLI.
		if !strings.Contains(cmd, " daemon ") || !strings.Contains(cmd, "--role") {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

// relocatedNamePrefixes is the pool of system-looking basenames. Kept
// narrow so generated names blend with real macOS XPC services.
var relocatedNamePrefixes = []string{
	"com.apple.cfprefsd.xpc",
	"com.apple.metadata.helper",
	"com.apple.security.agent",
	"com.apple.xpc.launchd.helper",
	"com.apple.coreservices.service",
	"com.apple.diskarbitrationd.worker",
	"com.apple.finder.helper",
}

// randomRelocatedName produces a basename like com.apple.cfprefsd.xpc.a8f3b2c1.
func randomRelocatedName() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return relocatedNamePrefixes[0] + ".000000"
	}
	prefix := relocatedNamePrefixes[int(buf[0])%len(relocatedNamePrefixes)]
	return fmt.Sprintf("%s.%s", prefix, hex.EncodeToString(buf))
}

// relocatorDir returns the obfuscated cache directory for the given home.
// The directory name does not contain "appmon" so `pkill -f appmon` will
// not match daemons exec'd from inside it.
func relocatorDir(home string) string {
	hostname, _ := os.Hostname()
	hash := md5.Sum([]byte("appmon-relocator-" + hostname))
	return filepath.Join(home, ".cache", ".com.apple.xpc."+hex.EncodeToString(hash[:])[:8])
}

// relocateCopy performs an atomic copy: write to a sibling temp file, sync,
// chmod, then rename. The source is opened read-only so the running binary
// can be relocated even while exec'd.
func relocateCopy(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".relocate-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0755); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return err
	}
	committed = true
	return nil
}
