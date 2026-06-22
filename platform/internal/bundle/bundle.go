// Package bundle embeds the plugin binaries + manifests that ship with
// the platform and extracts them to a per-install plugin root on
// startup, so a fresh platform deploy works zero-config with a known
// set of plugins.
//
// Why bundling: the daemon downloads a SINGLE platform asset from GH
// Releases. Shipping plugins as separate assets would require teaching
// the daemon to fetch a manifest of assets. Bundling keeps the daemon
// contract unchanged (one asset = one platform binary, plugins ride
// along inside it). Tradeoff: platform releases must include every
// plugin's binary for the target os/arch; plugin updates require a
// platform release. Acceptable for now — see daemon_design.md.
//
// Bundled plugins live under data/<plugin>/ in this package's source
// tree. The build script writes them there before `go build` so they
// get embedded.
package bundle

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:data
var fsys embed.FS

// ExtractTo extracts the bundled plugins into pluginRoot. A file is
// (over)written only when the on-disk content differs from the embedded
// content (cheap content hash compare), so this is safe to call on
// every platform startup and won't churn the disk or yank an
// in-flight plugin binary out from under itself.
//
// Plugin binaries are written 0o755; everything else 0o644.
// pluginRoot is created if absent.
func ExtractTo(pluginRoot string) (extracted []string, err error) {
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		return nil, err
	}
	walkErr := fs.WalkDir(fsys, "data", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Strip the leading "data/" so the on-disk layout matches the
		// platform's plugin discovery (one subdir per plugin).
		rel := strings.TrimPrefix(path, "data")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}
		target := filepath.Join(pluginRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fsys.ReadFile(path)
		if err != nil {
			return err
		}
		wrote, err := reconcileFile(target, rel, data)
		if err != nil {
			return err
		}
		if wrote {
			extracted = append(extracted, rel)
		}
		return nil
	})
	if walkErr != nil {
		return extracted, walkErr
	}
	return extracted, nil
}

// VerifyOrRestore reconciles a SINGLE plugin's on-disk binaries against
// the genuine embedded copy, scoped to data/<subdir>. For each embedded
// file under that subdir it sha256-compares the on-disk content against
// the embedded content and, on mismatch, atomically restores the genuine
// version via writeAtomic (temp+chmod+rename in the same dir — safe even
// if the plugin is mid-run). Mode bits are repaired on the fast path.
//
// It returns restored=true if ANY file was rewritten — the signal the
// runner records as a tamper event. wantPrefix/gotPrefix are the first 12
// hex chars of the genuine (embedded) vs the on-disk (pre-restore) sha256
// of the FIRST mismatched file — enough to diagnose a tamper without ever
// revealing a path or label. They are empty when nothing was restored.
// This is the point-of-use integrity check (ADR-0019): the on-disk plugin
// is confirmed genuine immediately before it is run, so a swap that landed
// since the last reconcile sweep is caught and repaired before the
// stale/substitute binary can execute.
//
// Pure Go, no build tags. An empty/unknown subdir (no embedded files)
// returns restored=false, "", "", nil — a non-bundled plugin is simply not
// covered, never an error.
func VerifyOrRestore(pluginRoot, subdir string) (restored bool, wantPrefix, gotPrefix string, err error) {
	// Defensive guard: an empty or "."/".." subdir would make embedDir walk
	// the whole bundle (every plugin) instead of one plugin, turning a
	// point-of-use check into a full restore. That can only come from a bad
	// caller (discovery always passes a real subdir); fail loudly rather
	// than silently over-reaching.
	clean := filepath.Clean(subdir)
	if subdir == "" || clean == "." || clean == ".." || strings.Contains(clean, "/") {
		return false, "", "", fmt.Errorf("invalid plugin subdir %q", subdir)
	}

	embedDir := "data/" + subdir
	walkErr := fs.WalkDir(fsys, embedDir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			// Unknown subdir (not in the bundle): nothing to verify.
			if errors.Is(werr, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return werr
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(path, "data")
		rel = strings.TrimPrefix(rel, "/")
		data, rerr := fsys.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(pluginRoot, rel)
		// Capture the on-disk (pre-restore) sha of THIS file before
		// reconcileFile may overwrite it, so we can surface the mismatch
		// prefixes for the first file that differs.
		var diskSha [32]byte
		var haveDisk bool
		if existing, readErr := os.ReadFile(target); readErr == nil {
			diskSha = sha(existing)
			haveDisk = true
		}
		wrote, rerr := reconcileFile(target, rel, data)
		if rerr != nil {
			return rerr
		}
		if wrote {
			restored = true
			// Record prefixes for the FIRST mismatched file only — enough
			// for diagnostics; never a path. got is "" when the file was
			// absent on disk (a missing binary, not a content swap).
			if wantPrefix == "" {
				wantPrefix = shaPrefix(sha(data))
				if haveDisk {
					gotPrefix = shaPrefix(diskSha)
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		return restored, wantPrefix, gotPrefix, walkErr
	}
	return restored, wantPrefix, gotPrefix, nil
}

// shaPrefix renders the first 12 hex chars (6 bytes) of a sha256 digest —
// a non-actionable fingerprint for tamper diagnostics.
func shaPrefix(sum [32]byte) string { return hex.EncodeToString(sum[:6]) }

// reconcileFile writes the genuine embedded content to target when the
// on-disk content differs, repairing the expected mode on the same-content
// fast path. It reports wrote=true only when the file was (over)written.
// Shared by ExtractTo (full bundle) and VerifyOrRestore (one plugin) so
// the atomic-restore + mode-repair semantics are identical everywhere.
func reconcileFile(target, rel string, data []byte) (wrote bool, err error) {
	mode := os.FileMode(0o644)
	// Heuristic: anything WITHOUT a `.json`/`.txt`/`.yaml` extension
	// inside a plugin's own dir is the plugin binary → executable.
	base := filepath.Base(rel)
	if !strings.ContainsAny(base, ".") || strings.HasSuffix(base, ".sh") {
		mode = 0o755
	}
	// Same-content fast path: don't rewrite, but still repair the
	// expected mode bits — a plugin binary that lost its +x (e.g.
	// because someone ran chmod by hand) would silently fail to
	// exec without this. (Copilot review.)
	if existing, rerr := os.ReadFile(target); rerr == nil {
		if sha(existing) == sha(data) {
			if info, statErr := os.Stat(target); statErr == nil && info.Mode().Perm() != mode {
				if chErr := os.Chmod(target, mode); chErr != nil {
					return false, chErr
				}
			}
			return false, nil
		}
	}
	if err := writeAtomic(target, data, mode); err != nil {
		return false, err
	}
	return true, nil
}

// HasAny reports whether the bundle contains any plugin at all (useful
// for tests and an honest error if someone built the platform without
// running the bundling step).
func HasAny() bool {
	entries, err := fsys.ReadDir("data")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			return true
		}
	}
	return false
}

func sha(b []byte) [32]byte { return sha256.Sum256(b) }

// writeAtomic = tempfile + rename in the same dir. Preserves callers
// that hold open the old inode (no torn writes).
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".bundle.")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return errors.Join(fmt.Errorf("rename %s: %w", path, err))
	}
	return nil
}
