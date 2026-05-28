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
// persisted (baked into the plists); never re-randomized. The SAME base
// is shared by all three mesh roles — see [RoleLabel] for the per-role
// label scheme and the deferred-review note.
func RandomBase() string {
	return fmt.Sprintf("%s.%s.%s", pick(prefixes), pick(suffixes), randHex(4))
}

// RoleLabel is the SINGLE authoritative function that turns an install
// base + a role into a launchd label. Every label for the daemon mesh
// (roles "a"/"b") and the periodic ensurer/cron ("ensure") is produced
// here and nowhere else — osadapter.Spec.Label delegates to it.
//
// Current implementation (intentionally kept): all three roles share one
// random base, so the label set is:
//
//	<base>.a   <base>.b   <base>.ensure      e.g. com.apple.security.worker.ca800c0c.{a,b,ensure}
//
// Known trade-off (accepted for now): the shared prefix means finding one
// label reveals the other two via a prefix grep. This is acceptable under
// the casual-grade-friction philosophy (durable weight is the server, not
// secrecy). Deferred for future review — to switch to independent
// per-role random labels, change ONLY this function (and how the base(s)
// are persisted in osadapter.Spec). Tracked in:
// https://github.com/eliteGoblin/focusd/issues/20
func RoleLabel(base, role string) string {
	return base + "." + role
}

// HiddenWorkdir is a dotted, Apple-metadata-looking directory under the
// given Application Support root (hidden from casual Finder/`ls`). The
// root is mode-specific (user → ~/Library/..., system → /Library/...),
// so a user and a system install never share a directory.
func HiddenWorkdir(supportRoot string) string {
	return filepath.Join(supportRoot, "."+pick(prefixes)+"."+randHex(6))
}

// RandomBinaryName is the disguised basename pattern used for the
// daemon binary inside its hidden workdir, e.g.
// "com.apple.metadata.helper.7f3a2c4d" (randHex(4) yields 8 hex chars,
// not 4 — Copilot #7). Extracted as its own primitive so the
// self-update path can rotate the binary path on every update
// (macOS AMFI caches the CDHash per executable path, so re-using the
// same path defeats adhoc-resign + restart for the swap; see
// internal/osadapter/selfupdate.go).
func RandomBinaryName() string {
	return pick(prefixes) + "." + pick(suffixes) + "." + randHex(4)
}

// RelocateInto copies src into dir under a random disguised basename,
// 0755, and returns the new path (hard-link first; copy fallback).
func RelocateInto(src, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("relocate: mkdir %s: %w", dir, err)
	}
	dst := filepath.Join(dir, RandomBinaryName())
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
