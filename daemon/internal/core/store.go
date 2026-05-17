package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Store is the daemon's tiny on-disk state under the workdir. The
// daemon process is stateless; durable facts live in these files and
// are re-read every tick (kubelet : etcd shape).
//
//	<workdir>/version.json   {"desired":"v1"}      desired version
//	<workdir>/good           "v1"                  last-known-good
//	<workdir>/bad/<v>        (marker file)         crash-looped versions
//	<workdir>/bin/<v>/platform                     platform binaries
type Store struct{ Dir string }

type versionConfig struct {
	Desired string `json:"desired"`
}

func (s *Store) versionPath() string { return filepath.Join(s.Dir, "version.json") }
func (s *Store) goodPath() string    { return filepath.Join(s.Dir, "good") }
func (s *Store) badDir() string      { return filepath.Join(s.Dir, "bad") }

// BinPath is where the platform binary for version v lives.
func (s *Store) BinPath(v string) string {
	return filepath.Join(s.Dir, "bin", v, "platform")
}

// HaveBin reports whether the platform binary for v exists.
func (s *Store) HaveBin(v string) bool {
	fi, err := os.Stat(s.BinPath(v))
	return err == nil && !fi.IsDir()
}

// HaveConfig reports whether a desired version has been resolved.
func (s *Store) HaveConfig() bool {
	_, err := os.Stat(s.versionPath())
	return err == nil
}

// Desired returns the configured desired version ("" if none).
func (s *Store) Desired() string {
	b, err := os.ReadFile(s.versionPath())
	if err != nil {
		return ""
	}
	var c versionConfig
	if json.Unmarshal(b, &c) != nil {
		return ""
	}
	return c.Desired
}

// WriteDesired atomically records the desired version.
func (s *Store) WriteDesired(v string) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(versionConfig{Desired: v})
	return atomicWrite(s.versionPath(), b)
}

// Good / WriteGood track the last-known-good version.
func (s *Store) Good() string {
	b, err := os.ReadFile(s.goodPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (s *Store) WriteGood(v string) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	return atomicWrite(s.goodPath(), []byte(v))
}

// MarkBad records v as crash-looped (never run again).
func (s *Store) MarkBad(v string) error {
	if err := os.MkdirAll(s.badDir(), 0o755); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.badDir(), safe(v)), []byte("1"))
}

// BadSet returns all versions marked bad.
func (s *Store) BadSet() map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(s.badDir())
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			out[e.Name()] = true
		}
	}
	return out
}

func safe(v string) string {
	return strings.NewReplacer("/", "_", "..", "_", " ", "_").Replace(v)
}

// atomicWrite writes via temp + rename so a crash mid-write cannot
// corrupt state (the next tick repairs anyway).
func atomicWrite(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
