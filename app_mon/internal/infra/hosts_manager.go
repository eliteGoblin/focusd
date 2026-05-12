package infra

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// DefaultHostsPath is the OS hosts file. Overridable in tests.
	DefaultHostsPath = "/etc/hosts"

	// HostsBlockBegin / HostsBlockEnd bound the section managed by
	// appmon. Lines outside this section are preserved verbatim across
	// every rewrite — we never touch user-authored entries.
	HostsBlockBegin = "# BEGIN appmon-blocklist (DO NOT EDIT — managed by appmon)"
	HostsBlockEnd   = "# END appmon-blocklist"

	// hostsBlockTarget is the address each blocked hostname resolves to.
	// 0.0.0.0 is faster than 127.0.0.1 for blocking — most clients fail
	// the connection immediately rather than retrying on the loopback.
	hostsBlockTarget = "0.0.0.0"
)

// HostsManager owns the appmon-managed section of /etc/hosts. The whole
// section is replaced atomically when content drifts from the expected
// blocklist; lines outside the markers are preserved untouched.
//
// The manager is permission-blind: it just tries to write and returns
// the OS error. The watcher in user mode catches the resulting EACCES
// and degrades gracefully — DNS-layer protection only applies in
// system mode where the daemon runs as root.
type HostsManager struct {
	path string
}

// NewHostsManager constructs a manager against /etc/hosts.
func NewHostsManager() *HostsManager {
	return &HostsManager{path: DefaultHostsPath}
}

// NewHostsManagerWithPath constructs a manager against a custom path —
// used in tests so we never touch the real /etc/hosts.
func NewHostsManagerWithPath(path string) *HostsManager {
	return &HostsManager{path: path}
}

// EnsureBlocklist guarantees that /etc/hosts contains exactly the given
// hostnames inside the appmon-managed section. Returns:
//
//	changed=true  → the file was rewritten (caller should flush DNS cache)
//	changed=false → on-disk content already matched (no-op)
//	err           → IO / permission error (caller decides how to surface)
//
// Idempotent: calling repeatedly with the same list yields a single
// initial write, then nothing. Tamper-resistant: any edit inside the
// markers (additions, deletions, reorderings) is reverted on next call.
// Tamper-safe: edits outside the markers are preserved.
func (h *HostsManager) EnsureBlocklist(hosts []string) (changed bool, err error) {
	current, err := h.read()
	if err != nil {
		return false, err
	}
	desired := renderHostsFile(current, hosts)
	if bytes.Equal(current, desired) {
		return false, nil
	}
	if err := h.atomicWrite(desired); err != nil {
		return false, err
	}
	return true, nil
}

// ActiveBlocklist returns the hostnames currently inside the managed
// section. Used by the `appmon blocklist` CLI command to show users
// what is effective right now (vs. what's compiled in, in case the
// file is stale).
func (h *HostsManager) ActiveBlocklist() ([]string, error) {
	current, err := h.read()
	if err != nil {
		return nil, err
	}
	return extractManagedHosts(current), nil
}

// FlushDNSCache asks macOS to drop its DNS cache so new /etc/hosts
// entries take effect immediately rather than waiting up to a TTL.
// Best-effort: errors are logged by the caller, not returned, because
// the blocklist is already written by this point.
func (h *HostsManager) FlushDNSCache() error {
	if err := exec.Command("dscacheutil", "-flushcache").Run(); err != nil {
		return fmt.Errorf("dscacheutil flush: %w", err)
	}
	if err := exec.Command("killall", "-HUP", "mDNSResponder").Run(); err != nil {
		return fmt.Errorf("mDNSResponder HUP: %w", err)
	}
	return nil
}

// read returns the current /etc/hosts contents. A missing file is
// treated as empty — we'll create it on first write.
func (h *HostsManager) read() ([]byte, error) {
	data, err := os.ReadFile(h.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", h.path, err)
	}
	return data, nil
}

// atomicWrite installs new contents via temp-file + rename, preserving
// the 644 mode that /etc/hosts has by convention.
func (h *HostsManager) atomicWrite(data []byte) error {
	dir := filepath.Dir(h.path)
	tmp, err := os.CreateTemp(dir, ".hosts-appmon-*")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
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
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, h.path); err != nil {
		return err
	}
	committed = true
	return nil
}

// renderHostsFile returns the contents the hosts file should have given
// the existing file `current` and the desired managed `hosts` list. The
// managed block is rewritten; everything outside the markers is kept.
//
// If the file has no existing markers, the managed block is appended
// (with a leading blank line for readability). If markers exist, the
// section between them is replaced in place.
func renderHostsFile(current []byte, hosts []string) []byte {
	block := buildManagedBlock(hosts)

	beginIdx, endIdx := findMarkerRange(current)
	if beginIdx < 0 {
		// First-time install: append managed block to end of file.
		var out bytes.Buffer
		out.Write(current)
		if len(current) > 0 && !bytes.HasSuffix(current, []byte("\n")) {
			out.WriteByte('\n')
		}
		if len(current) > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(block)
		return out.Bytes()
	}

	// Replace existing managed section in place.
	var out bytes.Buffer
	out.Write(current[:beginIdx])
	out.WriteString(block)
	out.Write(current[endIdx:])
	return out.Bytes()
}

// findMarkerRange locates the byte range of the existing managed block.
// Returns (beginIdx, endIdx) where:
//   - beginIdx is the start of the BEGIN-marker line
//   - endIdx is just past the END-marker line's trailing newline
//
// Returns (-1, -1) when no markers are present.
func findMarkerRange(data []byte) (int, int) {
	beginMarker := []byte(HostsBlockBegin)
	endMarker := []byte(HostsBlockEnd)

	beginIdx := bytes.Index(data, beginMarker)
	if beginIdx < 0 {
		return -1, -1
	}
	// The marker is mid-line — back up to start of line for clean replacement.
	for beginIdx > 0 && data[beginIdx-1] != '\n' {
		beginIdx--
	}

	endIdx := bytes.Index(data[beginIdx:], endMarker)
	if endIdx < 0 {
		return -1, -1
	}
	endIdx += beginIdx + len(endMarker)
	// Consume the trailing newline so the next character starts a new line.
	if endIdx < len(data) && data[endIdx] == '\n' {
		endIdx++
	}
	return beginIdx, endIdx
}

// buildManagedBlock renders the block, including markers and a single
// trailing newline, given the list of hostnames to block. One hostname
// per line (rather than multiple aliases per line) because it keeps
// diffs readable and per-entry restores trivial.
func buildManagedBlock(hosts []string) string {
	var b strings.Builder
	b.WriteString(HostsBlockBegin)
	b.WriteString("\n")
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		fmt.Fprintf(&b, "%s %s\n", hostsBlockTarget, h)
	}
	b.WriteString(HostsBlockEnd)
	b.WriteString("\n")
	return b.String()
}

// extractManagedHosts returns the hostnames currently listed inside
// the managed block. Returns nil if no markers are present.
func extractManagedHosts(data []byte) []string {
	beginIdx, _ := findMarkerRange(data)
	if beginIdx < 0 {
		return nil
	}
	// We want to scan only the body lines (between BEGIN and END
	// markers), so locate the END line's start within the block.
	endMarker := []byte(HostsBlockEnd)
	endLineStart := bytes.Index(data[beginIdx:], endMarker) + beginIdx

	body := data[beginIdx:endLineStart]
	var hosts []string
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		// Expected format: "0.0.0.0 <hostname>"
		if len(fields) < 2 {
			continue
		}
		hosts = append(hosts, fields[1:]...)
	}
	return hosts
}
