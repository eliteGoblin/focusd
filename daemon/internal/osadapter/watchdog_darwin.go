//go:build darwin

package osadapter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
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

// cronTab is the `crontab(1)` seam: read the whole table, write the whole
// table. We always read-modify-write the WHOLE crontab so EnsureWatchdog is
// idempotent (never blindly appends → no dup line on reinstall).
type cronTab interface {
	list() (string, error) // `crontab -l` (empty string when no crontab yet)
	replace(content string) error
}

// copyFS is the filesystem seam for the watchdog binary copy + its own
// hidden dir (separate from the mesh workdir).
type copyFS interface {
	relocateInto(src, dir string) (string, error) // place a disguised copy, return its path
	removeAll(dir string) error
}

// realCronTab shells out to `crontab`. An empty/no-crontab state surfaces as
// ("", nil): crontab exits non-zero with "no crontab for <user>" on stderr,
// which we treat as an empty table rather than an error.
type realCronTab struct{}

func (realCronTab) list() (string, error) {
	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		// `crontab -l` exits 1 when there is no crontab yet — treat as empty.
		return "", nil
	}
	return string(out), nil
}

func (realCronTab) replace(content string) error {
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab -: %w: %s", err, out)
	}
	return nil
}

// realCopyFS uses relocate (hard-link/copy under a disguised name) + a plain
// recursive remove of the copy dir.
type realCopyFS struct{}

func (realCopyFS) relocateInto(src, dir string) (string, error) {
	return relocate.RelocateInto(src, dir)
}
func (realCopyFS) removeAll(dir string) error { return os.RemoveAll(dir) }

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
// self-corrects on the next update. Best-effort; the old copy dir is left for
// the next refresh to overwrite (KISS — we do not chase orphans).
func RefreshWatchdog(m mode.Mode, newBinPath, desired string) error {
	return refreshWatchdog(m, newBinPath, desired, realCronTab{}, realCopyFS{})
}

func refreshWatchdog(
	m mode.Mode, newBinPath, desired string,
	ct cronTab, fs copyFS,
) error {
	cp, err := placeWatchdogCopy(m, newBinPath, fs)
	if err != nil {
		return err
	}
	cur, err := ct.list()
	if err != nil {
		return err
	}
	next := setCronLine(cur, cronLine(cp, desired))
	return ct.replace(next)
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
		_ = fs.removeAll(filepath.Dir(cp))
	}
	return nil
}

// WatchdogStatus reports the watchdog rail's liveness for the status line:
// cronPresent — our cron line exists; copyOK — the copy it points at is on
// disk and executable-ish (a plain stat). Bools only — no paths cross this
// boundary, so a caller like `daemon status` cannot leak the copy path.
func WatchdogStatus(m mode.Mode) (cronPresent, copyOK bool) {
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

// placeWatchdogCopy puts a fresh disguised copy of src into a NEW hidden dir
// under this mode's Application Support root — its OWN dir, NOT the mesh
// workdir (so a workdir wipe does not remove it). Returns the copy path.
func placeWatchdogCopy(m mode.Mode, src string, fs copyFS) (string, error) {
	home, _ := os.UserHomeDir()
	dir := relocate.HiddenWorkdir(mode.SupportRoot(m, home))
	return fs.relocateInto(src, dir)
}

// cronLine renders our cron entry for the given copy path + desired version.
func cronLine(copyPath, desired string) string {
	return fmt.Sprintf("* * * * * %s watchdog -v %s >/dev/null 2>&1", copyPath, desired)
}

// cronLineCopyPath returns the copy path baked into our cron line (the field
// between the 5 schedule fields and the `watchdog` token), or "" if no line
// with our marker is present. This is the SINGLE source of truth for the copy
// path — it is recovered from the crontab, never stored in the wiped workdir.
func cronLineCopyPath(crontab string) string {
	for _, ln := range strings.Split(crontab, "\n") {
		if !strings.Contains(ln, cronMarker) {
			continue
		}
		// Drop the leading 5 schedule fields ("* * * * *"); the next field is
		// the copy path, then the `watchdog` token.
		fields := strings.Fields(ln)
		// fields: [* * * * * <copyPath> watchdog -v <desired> ...]
		if len(fields) >= 7 && fields[6] == "watchdog" {
			return fields[5]
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
