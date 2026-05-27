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
		// Skip if already up-to-date.
		if existing, err := os.ReadFile(target); err == nil {
			if sha(existing) == sha(data) {
				return nil
			}
		}
		mode := os.FileMode(0o644)
		// Heuristic: anything WITHOUT a `.json`/`.txt`/`.yaml` extension
		// inside a plugin's own dir is the plugin binary → executable.
		base := filepath.Base(rel)
		if !strings.ContainsAny(base, ".") || strings.HasSuffix(base, ".sh") {
			mode = 0o755
		}
		if err := writeAtomic(target, data, mode); err != nil {
			return err
		}
		extracted = append(extracted, rel)
		return nil
	})
	if walkErr != nil {
		return extracted, walkErr
	}
	return extracted, nil
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
