// Package reconciler is the pure /etc/hosts block reconcile logic for the
// dns-block plugin. Idempotent: a noop tick costs nothing; drift (user
// edited /etc/hosts and removed the block) is restored on the next tick.
//
// Casual-grade DNS friction. A determined root user can still edit
// /etc/hosts to remove our entries — they'll be restored within one
// cron tick, which is the whole point of "the urge fades faster than
// the friction". This is *not* a security boundary.
package reconciler

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// Marker pair lets us find + replace JUST our block, preserving
	// anything else in /etc/hosts.
	BeginMarker = "# BEGIN focusd-blocklist (managed by dns-block plugin)"
	EndMarker   = "# END focusd-blocklist"

	defaultHostsPath = "/etc/hosts"
)

//go:embed all:data/*.txt
var defaultBlocklist embed.FS

// Reconciler holds the (small) reconcile config + test seams.
type Reconciler struct {
	// HostsPath defaults to /etc/hosts; tests override.
	HostsPath string
	// Domains explicitly overrides the embedded blocklist (tests + future
	// platform-config-driven mode).
	Domains []string
	// GetEUID is a test seam (defaults to os.Geteuid).
	GetEUID func() int
}

// Outcome reports what happened during one Reconcile call.
type Outcome struct {
	Changed bool   // false ⇒ /etc/hosts already matched desired
	Domains int    // count of domains in the rendered block
	Reason  string // short human description (e.g. "applied", "noop")
}

// Reconcile reads /etc/hosts, computes the desired content, writes
// atomically if drift is detected. Refuses if not running as root.
func (r *Reconciler) Reconcile() (Outcome, error) {
	if r.euid() != 0 {
		return Outcome{}, errors.New("dns-block: must run as root (writing /etc/hosts)")
	}

	domains, err := r.resolveDomains()
	if err != nil {
		return Outcome{}, fmt.Errorf("resolve domains: %w", err)
	}

	path := r.hostsPath()
	current, err := os.ReadFile(path)
	if err != nil {
		return Outcome{}, fmt.Errorf("read %s: %w", path, err)
	}

	desired := ReplaceBlock(string(current), RenderBlock(domains))
	if desired == string(current) {
		return Outcome{Changed: false, Domains: len(domains), Reason: "noop"}, nil
	}

	if err := atomicWrite(path, []byte(desired)); err != nil {
		return Outcome{}, fmt.Errorf("write %s: %w", path, err)
	}
	return Outcome{Changed: true, Domains: len(domains), Reason: "applied"}, nil
}

// RenderBlock builds the canonical `# BEGIN…END` block for a domain set.
// Exported so tests can build expected strings without re-encoding the
// marker format inline.
func RenderBlock(domains []string) string {
	var b strings.Builder
	b.WriteString(BeginMarker + "\n")
	for _, d := range domains {
		fmt.Fprintf(&b, "0.0.0.0 %s\n", d)
	}
	b.WriteString(EndMarker + "\n")
	return b.String()
}

// ReplaceBlock returns content with our block replaced by desired. If
// the BEGIN marker is absent, desired is appended after a blank line.
// Malformed (BEGIN without END) is healed in the same write: the
// orphaned BEGIN line and any trailing leftover are dropped before the
// fresh block is appended, so a single tick fully cleans the file
// rather than leaving a stray marker for the next tick to confuse.
func ReplaceBlock(content, desired string) string {
	bi := strings.Index(content, BeginMarker)
	if bi < 0 {
		return appendBlock(content, desired)
	}
	// Find END marker AFTER the BEGIN marker.
	tail := content[bi:]
	ei := strings.Index(tail, EndMarker)
	if ei < 0 {
		// Malformed: drop the orphaned BEGIN (and everything that
		// follows it, since we can't tell where the lost block
		// ended) before appending a fresh, well-formed block. The
		// user's pre-BEGIN content is preserved.
		return appendBlock(strings.TrimRight(content[:bi], "\n"), desired)
	}
	// Include the newline that follows END (if any).
	endAbs := bi + ei + len(EndMarker)
	if endAbs < len(content) && content[endAbs] == '\n' {
		endAbs++
	}
	before := strings.TrimRight(content[:bi], "\n")
	after := strings.TrimLeft(content[endAbs:], "\n")
	out := before
	if out != "" {
		out += "\n\n"
	}
	out += desired
	if after != "" {
		out += "\n" + after
	}
	return out
}

func appendBlock(content, desired string) string {
	if content == "" {
		return desired
	}
	sep := "\n"
	if !strings.HasSuffix(content, "\n") {
		sep = "\n\n"
	}
	return content + sep + desired
}

func (r *Reconciler) euid() int {
	if r.GetEUID != nil {
		return r.GetEUID()
	}
	return os.Geteuid()
}

func (r *Reconciler) hostsPath() string {
	if r.HostsPath != "" {
		return r.HostsPath
	}
	return defaultHostsPath
}

// resolveDomains returns explicit Domains if set, otherwise reads every
// data/*.txt embedded file. Lines starting with # or blank are ignored.
// Output is dedup'd and sorted for stable rendering.
func (r *Reconciler) resolveDomains() ([]string, error) {
	if len(r.Domains) > 0 {
		return uniqueSorted(r.Domains), nil
	}
	entries, err := defaultBlocklist.ReadDir("data")
	if err != nil {
		return nil, fmt.Errorf("read embedded data: %w", err)
	}
	var all []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		b, err := defaultBlocklist.ReadFile("data/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read data/%s: %w", e.Name(), err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			all = append(all, line)
		}
	}
	return uniqueSorted(all), nil
}

func uniqueSorted(s []string) []string {
	seen := make(map[string]struct{}, len(s))
	out := make([]string, 0, len(s))
	for _, x := range s {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}

// atomicWrite writes data to path via a tempfile + rename, preserving
// the destination's mode (and ownership if changeable). The rename is
// the only operation other readers see, so /etc/hosts is never in a
// half-written state.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".hosts.dnsblock.")
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
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	// Match destination's mode (default to 0644 if stat fails).
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
