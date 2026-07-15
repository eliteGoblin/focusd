//go:build darwin

package osadapter

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/platdir"
	"github.com/eliteGoblin/focusd/daemon/internal/relocate"
)

// FEATURE 12 / ADR-0016: the out-of-band watchdog rail. A root cron entry
// runs a SECOND copy of the daemon binary — placed OUTSIDE the mesh workdir
// so a total workdir wipe does not remove it — once a minute via `daemon
// watchdog`. That subcommand checks for a healthy mesh and, if absent,
// re-installs LOCALLY (no network fetch). This file is the darwin half of
// the seam; watchdog_other.go is the non-darwin no-op.
//
// Everything OS-specific (the `crontab` exec, the copy-dir filesystem) sits
// behind tiny interfaces (cronTab, copyFS) so the read-modify-write
// idempotency of EnsureWatchdog is unit-testable without touching the real
// crontab.

// cronMarker is the substring that identifies OUR cron line inside the
// crontab. The line shape is (5 schedule fields, then copy path, then the
// subcommand): "<sched> <copyPath> watchdog -v <desired> >/dev/null 2>&1".
// We match on " watchdog -v " (the subcommand + its flag), which is specific
// enough not to collide with an unrelated user cron line yet does not read as
// "focusd" to a casual `crontab -l`.
const cronMarker = " watchdog -v "

// cronExecTimeout caps every `crontab(1)` exec. EnsureWatchdog now runs on
// every mesh reconcile tick, so a hung `crontab` (e.g. a stuck mail prompt,
// a wedged cron daemon) must not block the tick indefinitely.
const cronExecTimeout = 5 * time.Second

// validVersionTagRE is a minimal, local semver-tag check (KISS): the version
// baked into the cron line MUST be non-empty and look like `v1.2.3` (with an
// optional trailing pre-release/build suffix). The authoritative validator
// lives in cmd/daemon (versionTagRE); osadapter cannot import the main
// package, so we keep a tiny duplicate here. An empty/garbage version must
// never be written into the cron line — a present-but-useless line can never
// rebuild the mesh (it would pin a version the watchdog refuses).
var validVersionTagRE = regexp.MustCompile(`^v\d+\.\d+\.\d+`)

func validVersionTag(s string) bool { return s != "" && validVersionTagRE.MatchString(s) }

// cronTab is the `crontab(1)` seam: read the whole table, write the whole
// table. We always read-modify-write the WHOLE crontab so EnsureWatchdog is
// idempotent (never blindly appends → no dup line on reinstall).
type cronTab interface {
	list() (string, error) // `crontab -l` (empty string when no crontab yet)
	replace(content string) error
}

// copyFS is the filesystem seam for the watchdog binary copy + its own
// hidden dir (separate from the mesh workdir).
//
// FEATURE 26: place now owns EXCLUSIVE creation of the copy dir (so it can never
// adopt a real app folder now that disguised names blend as ordinary apps) and
// marks it with the watchdog-copy content sentinel; removeCopyDir gates its delete
// on that positive match, so neither a name collision nor a tampered cron path can
// wipe a real folder.
type copyFS interface {
	// place puts a fresh disguised copy of src into a NEW exclusively-created,
	// content-marked hidden dir under supportRoot; returns the copy path.
	place(src, supportRoot string) (string, error)
	// removeCopyDir removes dir ONLY if it is a watchdog-copy dir this package
	// created (positive content sentinel). Best-effort.
	removeCopyDir(dir string) error
}

// realCronTab shells out to `crontab`. An empty/no-crontab state surfaces as
// ("", nil): crontab exits non-zero with "no crontab for <user>" on stderr,
// which we treat as an empty table rather than an error.
type realCronTab struct{}

// noCrontabRE matches the "no crontab for <user>" message crontab(1) prints
// (to stderr) when the calling user has no crontab yet. ONLY that specific
// empty-table case is treated as ("", nil); any other non-zero exit is a real
// error we surface rather than silently swallow (a swallowed permission/IO
// error would read as "no rail present" and trigger a needless rewrite).
var noCrontabRE = regexp.MustCompile(`(?i)no crontab for`)

func (realCronTab) list() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cronExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "crontab", "-l")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return string(out), nil
	}
	// "no crontab for <user>" → an empty table, not an error.
	if noCrontabRE.MatchString(stderr.String()) {
		return "", nil
	}
	return "", fmt.Errorf("crontab -l: %w: %s", err, strings.TrimSpace(stderr.String()))
}

func (realCronTab) replace(content string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cronExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab -: %w: %s", err, out)
	}
	return nil
}

// realCopyFS EXCLUSIVELY creates a fresh disguised dir (never adopts a real app
// folder), places the copy under a disguised name, and marks the dir with the
// watchdog-copy content sentinel. removeCopyDir deletes a dir only after a
// positive sentinel match.
type realCopyFS struct{}

func (realCopyFS) place(src, supportRoot string) (string, error) {
	dir, err := relocate.FreshHiddenDir(supportRoot) // os.Mkdir — never adopts
	if err != nil {
		return "", err
	}
	cp, err := relocate.RelocateInto(src, dir) // dir already exists (MkdirAll no-op)
	if err != nil {
		return "", err
	}
	platdir.MarkWatchdogCopy(dir) // so removeCopyDir can positively confirm ownership
	return cp, nil
}

// removeCopyDir removes dir ONLY when it carries the watchdog-copy content
// sentinel — a positive proof the dir is ours. A tampered cron path or a name
// collision with a real app folder (which has no sentinel) is skipped, so the
// teardown can never RemoveAll a real folder. A pre-FEATURE-26 copy dir (no
// sentinel) is likewise skipped — it leaks as harmless disk residue rather than
// risk a wrong delete.
func (realCopyFS) removeCopyDir(dir string) error {
	if !platdir.IsWatchdogCopy(dir) {
		return nil
	}
	return os.RemoveAll(dir)
}

// EnsureWatchdog writes the root cron line iff absent. It read-modify-writes
// the WHOLE crontab so a reinstall never duplicates the line. copyPath is the
// watchdog binary copy to invoke; when empty, EnsureWatchdog recovers the
// path from an existing cron line, or — if none exists — places a fresh copy
// of the CURRENT executable into its own hidden dir first. This "place if
// missing" form lets the mesh worker call EnsureWatchdog(mode, "", desired)
// as a one-liner.
func EnsureWatchdog(m mode.Mode, copyPath, desired string) error {
	return ensureWatchdog(m, copyPath, desired, realCronTab{}, realCopyFS{}, os.Executable)
}

// ensureWatchdog is the injectable core (seams: cron table, copy fs, the
// self-path lookup) so the idempotency is unit-tested with no real crontab.
func ensureWatchdog(
	m mode.Mode, copyPath, desired string,
	ct cronTab, fs copyFS, selfExec func() (string, error),
) error {
	// HIGH-1: never write a cron line pinning an empty/garbage version. Such a
	// "present-but-useless" line satisfies the presence check yet can never
	// rebuild the mesh (the watchdog subcommand refuses a non-semver -v).
	// Returning an error is safe: every caller is best-effort (log + swallow).
	if !validVersionTag(desired) {
		return fmt.Errorf("watchdog: refusing cron line with invalid version %q", desired)
	}
	cur, err := ct.list()
	if err != nil {
		return err
	}
	// Already present? Read-modify-write idempotency: a line with our marker
	// means the rail is up — nothing to do.
	if cronLineCopyPath(cur) != "" {
		return nil
	}
	// No line yet. Resolve the copy path: caller-supplied, else place a fresh
	// disguised copy of the current binary into its OWN hidden dir.
	if copyPath == "" {
		self, serr := selfExec()
		if serr != nil {
			return serr
		}
		cp, perr := placeWatchdogCopy(m, self, fs)
		if perr != nil {
			return perr
		}
		copyPath = cp
	}
	next := appendCronLine(cur, cronLine(copyPath, desired))
	return ct.replace(next)
}

// RefreshWatchdog places a FRESH disguised copy of newBinPath into its own
// hidden dir, then rewrites the cron line to point at the new copy (and the
// given desired). Called after a self-update so a stale watchdog copy
// self-corrects on the next update. The OLD copy's dir is removed after the
// cron rewrite so repeated self-updates do not accumulate stale watchdog copy
// dirs. Best-effort.
func RefreshWatchdog(m mode.Mode, newBinPath, desired string) error {
	return refreshWatchdog(m, newBinPath, desired, realCronTab{}, realCopyFS{})
}

func refreshWatchdog(
	m mode.Mode, newBinPath, desired string,
	ct cronTab, fs copyFS,
) error {
	// HIGH-1: same guard as ensureWatchdog — an invalid/empty version must
	// never be written into the (rewritten) cron line.
	if !validVersionTag(desired) {
		return fmt.Errorf("watchdog: refusing cron line with invalid version %q", desired)
	}
	cur, err := ct.list()
	if err != nil {
		return err
	}
	// Recover the OLD copy path BEFORE we place the new copy / rewrite the
	// line, so we can clean up its parent dir afterwards.
	oldCopy := cronLineCopyPath(cur)
	cp, err := placeWatchdogCopy(m, newBinPath, fs)
	if err != nil {
		return err
	}
	next := setCronLine(cur, cronLine(cp, desired))
	if rerr := ct.replace(next); rerr != nil {
		return rerr
	}
	// Remove the old copy's parent dir (its OWN hidden dir) iff it differs
	// from the freshly placed one — so self-updates don't leave orphan dirs.
	if oldCopy != "" && filepath.Dir(oldCopy) != filepath.Dir(cp) {
		_ = fs.removeCopyDir(filepath.Dir(oldCopy))
	}
	return nil
}

// RemoveWatchdog strips our cron line + removes the copy dir. Best-effort:
// the copy dir is recovered from the cron line's copy path (its parent dir).
func RemoveWatchdog(m mode.Mode) error {
	return removeWatchdog(m, realCronTab{}, realCopyFS{})
}

func removeWatchdog(m mode.Mode, ct cronTab, fs copyFS) error {
	cur, err := ct.list()
	if err != nil {
		return err
	}
	cp := cronLineCopyPath(cur)
	stripped := stripCronLine(cur)
	if stripped != cur {
		if rerr := ct.replace(stripped); rerr != nil {
			return rerr
		}
	}
	if cp != "" {
		// The copy lives in its own hidden dir; remove that whole dir.
		_ = fs.removeCopyDir(filepath.Dir(cp))
	}
	return nil
}

// WatchdogStatus reports the watchdog rail's liveness for the status line:
// cronPresent — our cron line exists; copyOK — the copy it points at is on
// disk and executable-ish (a plain stat). Bools only — no paths cross this
// boundary, so a caller like `daemon status` cannot leak the copy path.
func WatchdogStatus(m mode.Mode) (cronPresent, copyOK bool) {
	_ = m // mode is part of the public seam shape but unused: the cron line is
	// per-user (the invoking root crontab), recovered by marker, not by mode.
	return watchdogStatus(realCronTab{})
}

func watchdogStatus(ct cronTab) (cronPresent, copyOK bool) {
	cur, err := ct.list()
	if err != nil {
		return false, false
	}
	cp := cronLineCopyPath(cur)
	if cp == "" {
		return false, false
	}
	if info, serr := os.Stat(cp); serr == nil && !info.IsDir() {
		return true, true
	}
	return true, false
}

// placeWatchdogCopy puts a fresh disguised copy of src into a NEW exclusively-
// created, content-marked hidden dir under this mode's Application Support root —
// its OWN dir, NOT the mesh workdir (so a workdir wipe does not remove it).
// Returns the copy path. The exclusive create (in fs.place) is what stops a
// blended disguise name from ever adopting a real app folder.
func placeWatchdogCopy(m mode.Mode, src string, fs copyFS) (string, error) {
	home, _ := os.UserHomeDir()
	return fs.place(src, mode.SupportRoot(m, home))
}

// cronLine renders our cron entry for the given copy path + desired version.
// The copy path and version are single-quoted defensively so a home dir
// containing a space (e.g. `/Users/Some One/...`) survives the shell that cron
// uses to run the line — without quoting, such a path would be split into two
// argv words and the watchdog would be invoked with the wrong binary.
func cronLine(copyPath, desired string) string {
	return fmt.Sprintf("* * * * * '%s' watchdog -v '%s' >/dev/null 2>&1", copyPath, desired)
}

// cronCopyPathRE recovers the copy path from our cron line: it is the token
// between the 5 schedule fields and the ` watchdog -v ` marker. The path may
// be single-quoted (new lines) or bare (legacy lines), so both forms are
// captured. This is the SINGLE source of truth for the copy path — recovered
// from the crontab, never stored in the wiped workdir. Quoting the path means
// it may contain spaces, so a positional `strings.Fields` split no longer
// works; this anchored regex handles both quoted and bare forms.
var cronCopyPathRE = regexp.MustCompile(`^\S+\s+\S+\s+\S+\s+\S+\s+\S+\s+(?:'([^']*)'|(\S+)) watchdog -v `)

// cronLineCopyPath returns the copy path baked into our cron line, or "" if no
// line with our marker is present.
func cronLineCopyPath(crontab string) string {
	for _, ln := range strings.Split(crontab, "\n") {
		if !strings.Contains(ln, cronMarker) {
			continue
		}
		if m := cronCopyPathRE.FindStringSubmatch(ln); m != nil {
			if m[1] != "" {
				return m[1] // quoted form
			}
			return m[2] // bare (legacy) form
		}
	}
	return ""
}

// stripCronLine returns crontab with our marker line removed (idempotent if
// absent). Other lines + their order are preserved.
func stripCronLine(crontab string) string {
	if crontab == "" {
		return ""
	}
	var keep []string
	for _, ln := range strings.Split(crontab, "\n") {
		if strings.Contains(ln, cronMarker) {
			continue
		}
		keep = append(keep, ln)
	}
	return strings.Join(keep, "\n")
}

// setCronLine replaces our existing marker line (if any) with line, else
// appends it — so a refresh rewrites in place rather than duplicating.
func setCronLine(crontab, line string) string {
	return appendCronLine(stripCronLine(crontab), line)
}

// appendCronLine adds line to crontab with exactly one trailing newline,
// tolerating an empty or newline-padded existing table.
func appendCronLine(crontab, line string) string {
	cur := strings.TrimRight(crontab, "\n")
	if cur == "" {
		return line + "\n"
	}
	return cur + "\n" + line + "\n"
}
