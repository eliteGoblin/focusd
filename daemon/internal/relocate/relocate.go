// Package relocate produces disguised, per-install random names/paths and
// copies the daemon binary into a hidden workdir, so there is no fixed
// "focusd" string to grep/kill (daemon_design.md §6). Ported in spirit
// from app_mon v0.6.1's relocator/obfuscator.
//
// Casual-grade friction only (a determined/AI user reading a plist still
// learns the path) — the durable commitment weight is the server.
package relocate

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var prefixes = []string{
	"com.apple.metadata", "com.apple.cfprefsd", "com.apple.xpc",
	"com.apple.security", "com.apple.coreservices", "com.apple.spotlight",
}
var suffixes = []string{"helper", "worker", "xpc", "agent", "service"}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func pick(s []string) string {
	b := make([]byte, 1)
	_, _ = rand.Read(b)
	return s[int(b[0])%len(s)]
}

// RandomBase is a disguised, Apple-looking launchd label base, e.g.
// "com.apple.metadata.helper.7f3a2c11". Generated once at install and
// persisted (baked into the plists); never re-randomized.
func RandomBase() string {
	return fmt.Sprintf("%s.%s.%s", pick(prefixes), pick(suffixes), randHex(4))
}

// HiddenWorkdir is a dotted, Apple-metadata-looking directory under the
// user's Application Support (hidden from casual Finder/`ls`).
func HiddenWorkdir(home string) string {
	return filepath.Join(home, "Library", "Application Support",
		"."+pick(prefixes)+"."+randHex(6))
}

// RelocateInto copies src into dir under a random disguised basename,
// 0755, and returns the new path (hard-link first; copy fallback).
func RelocateInto(src, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("relocate: mkdir %s: %w", dir, err)
	}
	dst := filepath.Join(dir, pick(prefixes)+"."+pick(suffixes)+"."+randHex(4))
	if err := os.Link(src, dst); err == nil {
		_ = os.Chmod(dst, 0o755)
		return dst, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}
