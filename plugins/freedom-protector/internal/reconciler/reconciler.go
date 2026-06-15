// Package reconciler is the core logic for the freedom-protector plugin:
// keep the third-party Freedom focus app (Freedom.to) and its proxy
// process alive, and make a best-effort attempt to keep Freedom's
// macOS background/login item enabled.
//
// On each reconcile pass it:
//   - scans running processes for the Freedom main app and FreedomProxy;
//   - relaunches whichever is down (the proxy with its expected args);
//   - records a best-effort login-item note (see loginItemNote for the
//     honest limitation — there is no reliable public scriptable setter).
//
// The reconcile is idempotent (only relaunches what is down), bounded
// (every external launch runs under a timeout so the job never hangs),
// and skips cleanly when Freedom is not installed. The only OS-bound
// inputs are the process lister and the launcher, both behind interface
// seams so tests inject fakes and nothing real is touched.
package reconciler

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Grounded defaults observed on the live machine (see FEATURE 11). These
// are KISS hardcoded discovered values, optionally overridable via plugin
// config (see Options).
const (
	// DefaultAppPath is the Freedom application bundle.
	DefaultAppPath = "/Applications/Freedom.app"
	// DefaultAppProcess is the absolute path of the main app executable.
	DefaultAppProcess = "/Applications/Freedom.app/Contents/MacOS/Freedom"
	// DefaultProxyProcess is the absolute path of the proxy executable.
	DefaultProxyProcess = "/Applications/Freedom.app/Contents/MacOS/FreedomProxy"
	// DefaultProxyPort / DefaultProxyRPCPort are the proxy's expected args.
	DefaultProxyPort    = "7769"
	DefaultProxyRPCPort = "7770"

	// launchTimeout bounds every external launch so a hanging relaunch can
	// never stall the reconcile loop (acceptance #2).
	launchTimeout = 10 * time.Second
)

// procView is the minimal projection of a running process the reconciler
// needs: its pid plus the executable path/name to match against targets.
type procView struct {
	PID  int
	Path string // absolute executable path when available
	Name string // basename fallback when Path is empty
}

// procLister enumerates running processes. The real implementation reads
// the live process table; tests inject a fake.
type procLister func() ([]procView, error)

// launcher starts a detached process described by name + args, bounded by
// ctx. The real implementation shells out (open / direct exec); tests
// inject a fake that records the call without launching anything.
type launcher func(ctx context.Context, name string, args ...string) error

// Options are the optional config knobs (all default to the grounded
// constants when empty). Kept minimal — KISS.
type Options struct {
	AppPath      string
	AppProcess   string
	ProxyProcess string
	ProxyPort    string
	ProxyRPCPort string
}

// Reconciler holds the OS seams and resolved target paths. Construct it
// with New; tests overwrite list/launch directly.
type Reconciler struct {
	appPath      string
	appProcess   string
	proxyProcess string
	proxyPort    string
	proxyRPCPort string

	list   procLister
	launch launcher

	// stat reports whether the Freedom app bundle exists on disk. Behind
	// a seam so the "Freedom absent => benign skip" path is testable.
	stat func(path string) bool
}

// New builds a Reconciler, filling any empty Options field with its
// grounded default and wiring the real OS seams.
func New(opts Options) *Reconciler {
	r := &Reconciler{
		appPath:      orDefault(opts.AppPath, DefaultAppPath),
		appProcess:   orDefault(opts.AppProcess, DefaultAppProcess),
		proxyProcess: orDefault(opts.ProxyProcess, DefaultProxyProcess),
		proxyPort:    orDefault(opts.ProxyPort, DefaultProxyPort),
		proxyRPCPort: orDefault(opts.ProxyRPCPort, DefaultProxyRPCPort),
		list:         listProcesses,
		launch:       launchDetached,
		stat:         pathExists,
	}
	return r
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// Outcome summarises one reconcile pass. It is the shape the command
// emits as JSON to stdout.
type Outcome struct {
	Skipped       bool     `json:"skipped"`               // Freedom not installed
	SkipReason    string   `json:"skip_reason,omitempty"` // why skipped
	Scanned       int      `json:"scanned"`               // processes inspected
	AppRunning    bool     `json:"app_running"`
	ProxyRunning  bool     `json:"proxy_running"`
	Relaunched    []string `json:"relaunched"`       // targets relaunched this pass ("app","proxy")
	Failed        []string `json:"failed,omitempty"` // "target: reason" for launch failures
	LoginItemNote string   `json:"login_item_note"`  // best-effort login-item status (honest)
}

// loginItemNote is the explicit, honest record for acceptance #3.
//
// Freedom's login item lives in the macOS background-item / SMAppService
// store (System Settings -> Login Items -> "Allow in the Background").
// That surface has NO reliable public scriptable setter: there is no
// supported `launchctl`/`sfltool`/SMAppService call to flip a *third
// party* app's background-item back on from outside that app. So this
// plugin does NOT claim to re-enable it. The one indirect, legitimate
// nudge is that relaunching the app via `open -a Freedom` re-runs
// Freedom, which is the path through which Freedom itself can
// re-register its background item — but whether the toggle actually
// returns is NOT machine-verifiable here. We therefore make this an
// explicit, recorded best-effort no-op and require manual verification.
// Mirrors the FEATURE 10 Login-Items honest limitation.
const loginItemNote = "login-item re-enable: not scriptable (best-effort; " +
	"relaunch via `open -a Freedom` lets Freedom re-register its own " +
	"background item, but the System Settings \"Allow in the Background\" " +
	"toggle has no public scriptable setter — manual-verify)"

// Reconcile runs one idempotent pass. The error return is reserved for a
// hard failure of the underlying process enumeration; a failed *launch*
// is recorded in Outcome.Failed and does not abort the pass (controlled
// failure, surfaced to the caller for exit-code mapping).
func (r *Reconciler) Reconcile(ctx context.Context) (Outcome, error) {
	// Skip cleanly when Freedom is absent — never error or hang.
	if !r.stat(r.appPath) {
		return Outcome{
			Skipped:       true,
			SkipReason:    fmt.Sprintf("%s not present", r.appPath),
			LoginItemNote: loginItemNote,
		}, nil
	}

	procs, err := r.list()
	if err != nil {
		return Outcome{}, fmt.Errorf("enumerate processes: %w", err)
	}

	out := Outcome{Scanned: len(procs), LoginItemNote: loginItemNote}
	out.AppRunning = matchesAny(procs, r.appProcess)
	out.ProxyRunning = matchesAny(procs, r.proxyProcess)

	// Relaunch only what is down (idempotent).
	if !out.AppRunning {
		// `open -a` is the least-racy way to (re)launch a GUI .app: it is
		// idempotent (a no-op if already up) and routes through Launch
		// Services, and it is also the indirect path by which Freedom can
		// re-register its own background/login item (see loginItemNote).
		if err := r.runLaunch(ctx, "open", "-a", r.appPath); err != nil {
			out.Failed = append(out.Failed, fmt.Sprintf("app: %v", err))
		} else {
			out.Relaunched = append(out.Relaunched, "app")
		}
	}
	if !out.ProxyRunning {
		// The proxy is a plain helper binary, not a .app, so exec it
		// directly with its expected args.
		if err := r.runLaunch(ctx, r.proxyProcess,
			"-port", r.proxyPort, "-rpcport", r.proxyRPCPort); err != nil {
			out.Failed = append(out.Failed, fmt.Sprintf("proxy: %v", err))
		} else {
			out.Relaunched = append(out.Relaunched, "proxy")
		}
	}
	sort.Strings(out.Relaunched)
	return out, nil
}

// runLaunch bounds a single launch with launchTimeout so a hanging
// relaunch can never stall the reconcile loop (acceptance #2). A caller
// ctx that is already cancelled / has a shorter deadline is honoured.
func (r *Reconciler) runLaunch(ctx context.Context, name string, args ...string) error {
	cctx, cancel := context.WithTimeout(ctx, launchTimeout)
	defer cancel()
	return r.launch(cctx, name, args...)
}

// matchesAny reports whether any running process corresponds to target,
// which is an absolute executable path. It matches on the full path when
// the lister supplied one, else on the basename — so a process reported
// only by name still matches and "FreedomProxy" never matches "Freedom".
func matchesAny(procs []procView, target string) bool {
	base := filepath.Base(target)
	for _, p := range procs {
		if p.Path != "" {
			if p.Path == target {
				return true
			}
			continue
		}
		if p.Name != "" && filepath.Base(p.Name) == base {
			return true
		}
	}
	return false
}
