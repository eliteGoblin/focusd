// Package selfdaemon is the browser-monitor plugin's BEST-EFFORT, user-mode
// self-daemon tier (FEATURE 27): a Go port of
// utils/mac-browser-guard/browser_guard.py's install()/heal()/uninstall().
//
// It is only reachable via the daemon-install / self-tick / daemon-uninstall
// subcommands — NEVER from `run` (the platform-supervised plugin mode, which
// must never self-install or self-heal). Mode selection is argv-only; this
// package never probes launchd or the filesystem to "detect" its situation.
//
// Layout mirrors the Python util: two hidden, mutually-healing copies of the
// binary + a user LaunchAgent (~10s) + a cron fallback (~5m). Every tick HEALS
// first (restores any deleted piece from a survivor) then SCANS, so removing it
// means deleting ALL pieces inside the cron window. Entirely user-mode: no
// sudo, no root, no system domain.
//
// Every OS interaction (launchctl, crontab, reading the running binary, the
// scan) is an injected SEAM, so the whole lifecycle is unit-tested without
// touching the real launchd, cron, or install locations.
package selfdaemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Agent owns one self-daemon install. No real I/O happens except through the
// seam funcs and the Copies/Plist/Log paths — all of which tests point at a
// temp dir.
type Agent struct {
	Copies    []string // >=2 hidden binary copies (mutually healing)
	PlistPath string   // user LaunchAgent plist
	Label     string   // LaunchAgent label
	CronTag   string   // marker comment identifying our cron line
	LogPath   string   // stdout/stderr sink
	Interval  int      // LaunchAgent StartInterval (seconds)

	// Seams. DefaultAgent wires the real ones; tests wire fakes.
	ReadExecutable func() ([]byte, error)     // genuine bytes of THE RUNNING binary
	Launchctl      func(args ...string) error // best-effort launchctl
	ReadCrontab    func() (string, error)     // current crontab text ("" if none)
	WriteCrontab   func(string) error         // replace crontab
	Scan           func() int                 // one guard pass -> exit code
}

// Install (re)deploys the self-daemon from the running binary: force-overwrite
// every hidden copy + the plist, heal in any missing piece, then reload the
// LaunchAgent. Idempotent — running it over an existing install just refreshes
// it to the current binary.
func (a *Agent) Install() error {
	data, err := a.sourceBytes()
	if err != nil {
		return err
	}
	for _, c := range a.Copies {
		if err := writeFile(c, data, 0o755); err != nil {
			return fmt.Errorf("write copy %s: %w", c, err)
		}
	}
	if err := writeFile(a.PlistPath, []byte(a.plistXML()), 0o644); err != nil {
		return fmt.Errorf("write plist %s: %w", a.PlistPath, err)
	}
	if err := a.Heal(); err != nil {
		return err
	}
	_ = a.Launchctl("unload", a.PlistPath)
	_ = a.Launchctl("load", a.PlistPath)
	return nil
}

// Heal restores any deleted piece (copy, plist, cron line) from a survivor.
// Best-effort: if no genuine source bytes remain, it stays quiet rather than
// erroring, matching the Python util. Safe to call every tick.
func (a *Agent) Heal() error {
	data, err := a.sourceBytes()
	if err != nil {
		return nil // nothing to heal from — best-effort, stay quiet
	}
	for _, c := range a.Copies {
		if !fileExists(c) {
			if err := writeFile(c, data, 0o755); err != nil {
				return fmt.Errorf("heal copy %s: %w", c, err)
			}
		}
	}
	if !fileExists(a.PlistPath) {
		if err := writeFile(a.PlistPath, []byte(a.plistXML()), 0o644); err != nil {
			return fmt.Errorf("heal plist %s: %w", a.PlistPath, err)
		}
		_ = a.Launchctl("load", a.PlistPath)
	}
	// Only touch the crontab when we could READ it — writing "" back on a read
	// error would wipe the user's unrelated cron jobs. The cron rail is a
	// best-effort fallback, so a read failure just skips it this tick.
	if cur, rerr := a.ReadCrontab(); rerr == nil && !strings.Contains(cur, a.CronTag) {
		if werr := a.WriteCrontab(appendCron(cur, a.cronLine())); werr != nil {
			return fmt.Errorf("heal crontab: %w", werr)
		}
	}
	return nil
}

// Uninstall removes every piece: unload + remove the LaunchAgent, strip the
// cron line, delete the plist, log, and hidden copies. Tolerant of already-gone
// pieces (idempotent) so a partial state still cleans up fully.
func (a *Agent) Uninstall() error {
	_ = a.Launchctl("unload", a.PlistPath)
	_ = a.Launchctl("remove", a.Label) // label-based; works even if the plist is gone

	// Attempt EVERY removal even if one fails, so a single permission error on
	// one piece doesn't leave the rest (e.g. the hidden copies) behind. Collect
	// and return the aggregate.
	var errs []error
	if cur, err := a.ReadCrontab(); err == nil && strings.Contains(cur, a.CronTag) {
		if werr := a.WriteCrontab(removeCron(cur, a.CronTag)); werr != nil {
			errs = append(errs, fmt.Errorf("rewrite crontab: %w", werr))
		}
	}
	for _, p := range append([]string{a.PlistPath, a.LogPath}, a.Copies...) {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}
	return errors.Join(errs...)
}

// Tick is what the schedule invokes: heal first (best-effort), then scan.
// Returns the scan exit code (0 ok · 1 a kill failed · 2 runtime error).
func (a *Agent) Tick() int {
	// Heal failures must not block the scan, but they ARE actionable, so log
	// them to stderr (the LaunchAgent captures it to LogPath).
	if err := a.Heal(); err != nil {
		fmt.Fprintln(os.Stderr, "selfdaemon: heal:", err)
	}
	if a.Scan == nil {
		return 2
	}
	return a.Scan()
}

// sourceBytes returns the genuine binary bytes to (re)write copies with: the
// running executable if readable, else the first surviving copy. Mirrors the
// Python _source_bytes fallback, so heal works even when the running binary's
// own path was the one deleted.
func (a *Agent) sourceBytes() ([]byte, error) {
	if a.ReadExecutable != nil {
		if b, err := a.ReadExecutable(); err == nil && len(b) > 0 {
			return b, nil
		}
	}
	for _, c := range a.Copies {
		if b, err := os.ReadFile(c); err == nil && len(b) > 0 {
			return b, nil
		}
	}
	return nil, fmt.Errorf("selfdaemon: no genuine source bytes (executable and all copies unreadable)")
}

// plistXML renders the user LaunchAgent that runs the FIRST copy with the
// self-tick subcommand every Interval seconds.
func (a *Agent) plistXML() string {
	target := a.Copies[0]
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>%s</string><string>self-tick</string></array>
  <key>RunAtLoad</key><true/>
  <key>StartInterval</key><integer>%d</integer>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, xmlEscape(a.Label), xmlEscape(target), a.Interval, xmlEscape(a.LogPath), xmlEscape(a.LogPath))
}

// cronLine is the 5-minute fallback: it runs the LAST copy (the plist runs the
// first) so the two schedules key off different binaries.
func (a *Agent) cronLine() string {
	target := a.Copies[len(a.Copies)-1]
	return fmt.Sprintf("*/5 * * * * '%s' self-tick >/dev/null 2>&1  %s", target, a.CronTag)
}

func appendCron(cur, line string) string {
	if strings.TrimSpace(cur) == "" {
		return line + "\n"
	}
	return strings.TrimRight(cur, "\n") + "\n" + line + "\n"
}

func removeCron(cur, tag string) string {
	var keep []string
	for _, l := range strings.Split(cur, "\n") {
		if strings.Contains(l, tag) {
			continue
		}
		keep = append(keep, l)
	}
	out := strings.TrimRight(strings.Join(keep, "\n"), "\n")
	if out == "" {
		return ""
	}
	return out + "\n"
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// writeFile writes data to path ATOMICALLY (temp file in the same dir + rename)
// with the given mode. The LaunchAgent (~10s) and cron (~5m) are independent
// schedules that may each heal concurrently in separate processes; an atomic
// rename means a reader/executor of a copy never observes a torn/truncated
// binary, and a running copy keeps its old inode across the swap (safe on
// macOS). Mode is enforced even when the file pre-existed (a copy that lost +x
// must not silently fail to exec).
func writeFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sd-*")
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
		return err
	}
	return nil
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
