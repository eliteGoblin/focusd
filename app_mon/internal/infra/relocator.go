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

	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
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
