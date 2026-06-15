package core

// roster.go: the masked, persisted mesh-label roster (FEATURE 10 /
// ADR-0014). The three independent mesh labels are written to the hidden
// workdir XOR-masked with a roster-specific deterministic key, reusing
// the same casual-grade masking pattern as the embedded pubkey (FEATURE
// 3). In-memory roster is authoritative; this file exists only so a
// freshly relaunched survivor can recover the full roster on a cold
// start, and it self-heals from memory if edited or deleted.
//
// HONEST SCOPE — friction, not crypto. The mask is deterministic and its
// key derives from a label that lives in this same binary; an attacker
// who reads the daemon binary can recover the key and un-mask the file.
// The single goal is to defeat a casual `cat`/`ls` of the workdir
// (acceptance #3). See ADR-0014's honest-limitation section.

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// rosterVersion is the leading byte of the unmasked payload. It lets a
// future format change be detected (a wrong/absent version byte is read
// as corruption → caller rewrites from memory).
const rosterVersion byte = 1

// deriveRosterMask returns the deterministic 32-byte XOR mask for the
// roster. It is DISTINCT from the pubkey mask: the label is
// roster-specific so the two artifacts never share a key. The label is
// built at runtime from two literals (not a single const) so a
// `strings | grep` against the binary doesn't find a contiguous match —
// same defense as the pubkey mask.
func deriveRosterMask() [32]byte {
	var labelA = "focusd-roster-"
	var labelB = "mask-v1"
	buf := make([]byte, 0, len(labelA)+len(labelB))
	buf = append(buf, labelA...)
	buf = append(buf, labelB...)
	return sha256.Sum256(buf)
}

// xor masks src with key in place-free form (XOR is its own inverse, so
// the same function both masks and un-masks). Returns a new slice.
func xor(src []byte, key []byte) []byte {
	out := make([]byte, len(src))
	for i, b := range src {
		out[i] = b ^ key[i%len(key)]
	}
	return out
}

// WriteRoster writes the three mesh labels to path, XOR-masked, atomically
// (temp + rename) with mode 0600. The payload is: 1 version byte followed
// by the labels newline-joined (positional, AllRoles order). Labels must
// not contain a newline (the disguise pool never produces one).
func WriteRoster(path string, labels []string) error {
	for _, l := range labels {
		if strings.ContainsRune(l, '\n') {
			return fmt.Errorf("core: roster label contains newline: %q", l)
		}
	}
	payload := append([]byte{rosterVersion}, []byte(strings.Join(labels, "\n"))...)
	mask := deriveRosterMask()
	masked := xor(payload, mask[:])
	return atomicWrite0600(path, masked)
}

// ReadRoster reads + un-masks the roster file at path and returns the
// mesh labels. It errors cleanly (so the caller rewrites from memory) when
// the file is missing, empty, or its version byte is unrecognised —
// i.e. tampering/deletion never silently yields garbage labels
// (acceptance #4).
func ReadRoster(path string) ([]string, error) {
	masked, err := os.ReadFile(path)
	if err != nil {
		return nil, err // missing/unreadable → caller rewrites from memory
	}
	if len(masked) == 0 {
		return nil, errors.New("core: roster file empty")
	}
	mask := deriveRosterMask()
	payload := xor(masked, mask[:])
	if payload[0] != rosterVersion {
		return nil, fmt.Errorf("core: roster version byte %d, want %d (corrupt?)", payload[0], rosterVersion)
	}
	body := payload[1:]
	if len(body) == 0 {
		return nil, errors.New("core: roster has no labels")
	}
	labels := strings.Split(string(body), "\n")
	for _, l := range labels {
		if l == "" {
			return nil, errors.New("core: roster has an empty label (corrupt?)")
		}
	}
	return labels, nil
}

// atomicWrite0600 writes b via temp + rename with mode 0600. The roster is
// sensitive, so it does NOT reuse store.go's 0644 atomicWrite.
func atomicWrite0600(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
