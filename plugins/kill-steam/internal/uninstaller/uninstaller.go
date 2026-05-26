// Package uninstaller is the "kill-steam takes the gloves off" half of
// the plugin: when Steam.app is detected on disk, remove it AND the per-
// user Steam appdata, caches, logs, launchd helper and saved state — so
// a casual reinstall costs ~25 GB of Dota redownload + a Valve account
// login, instead of double-click-and-go.
//
// Cheap-detect-first: a tick with no Steam.app costs one os.Stat and
// returns. The expensive work only fires when Steam is actually present
// (i.e. on an install event), then noops forever after.
//
// Casual-grade friction, same as the rest of focusd. A determined user
// can reinstall again; this plugin will re-uninstall on the next tick.
package uninstaller

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// systemTarget is a literal path removed if present.
type systemTarget struct {
	Path string
	What string
}

// perUserTarget is a path relative to a home dir, removed if present in
// any user's home.
type perUserTarget struct {
	RelPath string
	What    string
}

// DefaultSystemTargets are the system-scoped Steam artifacts.
var DefaultSystemTargets = []systemTarget{
	{Path: "/Applications/Steam.app", What: "Steam application"},
}

// DefaultPerUserTargets are removed under every real user's home.
var DefaultPerUserTargets = []perUserTarget{
	{RelPath: "Library/Application Support/Steam", What: "Steam appdata (includes Dota 2)"},
	{RelPath: "Library/Caches/com.valvesoftware.steam", What: "Steam cache (bundle id)"},
	{RelPath: "Library/Caches/Steam", What: "Steam cache"},
	{RelPath: "Library/Logs/Steam", What: "Steam logs"},
	{RelPath: "Library/Saved Application State/com.valvesoftware.steam.savedState", What: "Steam saved state"},
	{RelPath: "Library/LaunchAgents/com.valvesoftware.steamclean.plist", What: "Steam launch agent (auto-reinstall vector)"},
	{RelPath: "Library/Preferences/com.valvesoftware.steam.plist", What: "Steam preferences"},
	{RelPath: "Library/Logs/DiagnosticReports", What: "Dota 2 crash reports (best effort glob)"}, // filtered by name
}

// Reconciler is the testable surface. Override AppPath / UsersDir for
// tests; defaults are the real macOS paths.
type Reconciler struct {
	// AppPath is the trigger / first-check. Default: /Applications/Steam.app.
	AppPath string
	// UsersDir is the dir holding per-user homes. Default: /Users.
	UsersDir string
	// System and PerUser default to Default*Targets unless overridden.
	System  []systemTarget
	PerUser []perUserTarget
}

// Outcome summarises a single Reconcile pass.
type Outcome struct {
	Detected bool     `json:"detected"`
	Removed  []string `json:"removed,omitempty"`
	Errors   []string `json:"errors,omitempty"`
	Reason   string   `json:"reason"`
}

// Detect is the cheap path: does Steam.app exist? Used as the gate
// before any expensive enumeration / removal. Steady-state noop ticks
// hit this and return in <1 ms.
func (r *Reconciler) Detect() bool {
	_, err := os.Stat(r.appPath())
	return err == nil
}

// Reconcile is the full pass: detect, then if found, sweep every known
// Steam artifact (system + per-user). Idempotent — noop when Steam is
// not installed; total removal when it is.
func (r *Reconciler) Reconcile() Outcome {
	o := Outcome{Detected: r.Detect()}
	if !o.Detected {
		o.Reason = "noop (no Steam.app)"
		return o
	}

	for _, t := range r.systemTargets() {
		r.tryRemove(t.Path, t.What, &o)
	}

	homes, err := r.findUserHomes()
	if err != nil {
		o.Errors = append(o.Errors, fmt.Sprintf("enumerate users: %v", err))
	}
	for _, home := range homes {
		for _, t := range r.perUserTargets() {
			full := filepath.Join(home, t.RelPath)
			// Special-case the DiagnosticReports dir — don't rm the dir,
			// just the dota2-*.ips files in it.
			if strings.HasSuffix(t.RelPath, "DiagnosticReports") {
				r.cleanCrashReports(full, &o)
				continue
			}
			r.tryRemove(full, t.What, &o)
		}
	}

	switch len(o.Removed) {
	case 0:
		o.Reason = "detected but nothing to remove (likely already partial)"
	default:
		o.Reason = fmt.Sprintf("removed %d artifact(s)", len(o.Removed))
	}
	return o
}

func (r *Reconciler) tryRemove(path, what string, o *Outcome) {
	if _, err := os.Stat(path); err != nil {
		return // not present
	}
	if err := os.RemoveAll(path); err != nil {
		o.Errors = append(o.Errors, fmt.Sprintf("%s (%s): %v", what, path, err))
		return
	}
	o.Removed = append(o.Removed, path)
}

func (r *Reconciler) cleanCrashReports(dir string, o *Outcome) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // dir doesn't exist or unreadable — fine
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(strings.ToLower(name), "dota2") {
			continue
		}
		full := filepath.Join(dir, name)
		if err := os.Remove(full); err == nil {
			o.Removed = append(o.Removed, full)
		}
	}
}

func (r *Reconciler) appPath() string {
	if r.AppPath != "" {
		return r.AppPath
	}
	return "/Applications/Steam.app"
}

func (r *Reconciler) usersDir() string {
	if r.UsersDir != "" {
		return r.UsersDir
	}
	return "/Users"
}

func (r *Reconciler) systemTargets() []systemTarget {
	if r.System != nil {
		return r.System
	}
	return DefaultSystemTargets
}

func (r *Reconciler) perUserTargets() []perUserTarget {
	if r.PerUser != nil {
		return r.PerUser
	}
	return DefaultPerUserTargets
}

// findUserHomes returns the home directories of real users on this Mac
// (skips dotted dirs, Shared, and anything that isn't a directory).
func (r *Reconciler) findUserHomes() ([]string, error) {
	entries, err := os.ReadDir(r.usersDir())
	if err != nil {
		return nil, err
	}
	var homes []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "Shared" {
			continue
		}
		homes = append(homes, filepath.Join(r.usersDir(), name))
	}
	if len(homes) == 0 {
		return nil, errors.New("no user homes found")
	}
	return homes, nil
}
