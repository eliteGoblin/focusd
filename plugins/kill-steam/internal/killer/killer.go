// Package killer terminates Steam/Dota2 processes. Ported from the
// app_mon v0.6.1 policy + process layer, including the v0.6.1 #17 fix:
// process names are matched EXACTLY (case-insensitive), never as a
// substring — substring matching killed Microsoft Teams via the "steam"
// inside "msteams".
//
// Name-only matching under-killed, though: Steam's helpers run under
// generic comm names (ipcserver, gameoverlayui, …) that no name list can
// enumerate, so they survived while the plugin still reported success —
// green over dead protection. This layer therefore ALSO matches by
// executable path: any process running from under the Steam bundle is a
// Steam process regardless of its comm name. An unrelated binary that
// merely shares a generic name (e.g. /usr/local/bin/ipcserver) is not
// under a Steam path and is left alone. After killing, the killer
// re-scans; any Steam process still present is reported as a survivor so
// a clean "ok" can never mask a live Steam.
package killer

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

// DefaultProcessNames is the built-in Steam + Dota2 process basename set
// (macOS). Mirrors app_mon SteamPolicy/Dota2Policy ProcessPatterns.
var DefaultProcessNames = []string{
	// Steam
	"Steam", "steam_osx", "steamwebhelper", "steamservice",
	"Steam Helper", "Steam Helper (GPU)", "Steam Helper (Renderer)",
	"Steam Helper (Plugin)",
	// Dota 2
	"dota2", "dota_osx64", "Dota 2", "dota2_launcher",
}

// DefaultSteamPathMarkers are executable-path substrings (matched
// case-insensitively) that identify a process as part of the Steam
// install regardless of its comm name. This is what catches the
// generically-named helpers (ipcserver, steamwebhelper, gameoverlayui,
// …) that a name-only match misses. A different app that happens to have
// a generic process name will not sit under one of these paths, so it is
// not false-matched.
var DefaultSteamPathMarkers = []string{
	"/steam.appbundle/",
	"/steam.app/contents/",
	"/library/application support/steam/",
}

// settleInterval is how long we wait between post-kill re-scans, and
// settleAttempts is how many times we re-check. Polling (rather than one
// fixed sleep) gives a SIGKILLed process time to be reaped before we count
// it as a survivor, while short-circuiting the moment it is confirmed gone
// — so a successful kill is never miscounted as "still alive". Max wait
// when a real survivor persists: settleInterval * settleAttempts (~1.5s).
const (
	settleInterval = 300 * time.Millisecond
	settleAttempts = 5
)

// Outcome summarises a kill pass.
type Outcome struct {
	Scanned    int      `json:"scanned"`
	KilledPIDs []int    `json:"killed_pids"`
	Failed     []string `json:"failed,omitempty"` // "pid: reason"
	// Survivors are target (Steam/Dota) procs still present after the
	// kill+settle window (kill didn't take / relaunch). A survivor can be a
	// name-matched Dota process, not only a Steam-bundle path. Excludes PIDs
	// already in Failed, so the two lists are disjoint.
	Survivors []int `json:"survivors,omitempty"`
	// RescanError is set when the post-kill re-scan itself could not run.
	// Verification could not confirm Steam is gone, so the caller MUST NOT
	// treat the pass as clean — an unverifiable pass is never green.
	RescanError string `json:"rescan_error,omitempty"`
}

// KilledCount is the number of processes successfully terminated.
func (o Outcome) KilledCount() int { return len(o.KilledPIDs) }

// procLister/procKiller are seams so tests don't touch real processes.
type procView struct {
	PID  int
	Name string
	Path string // executable path (best-effort; empty when unreadable)
}

type Killer struct {
	names    []string
	markers  []string
	list     func() ([]procView, error)
	killPID  func(pid int) error
	sleep    func(time.Duration)
	settle   time.Duration
	attempts int
}

// New builds a Killer. Empty names => DefaultProcessNames.
func New(names []string) *Killer {
	if len(names) == 0 {
		names = DefaultProcessNames
	}
	return &Killer{
		names:    names,
		markers:  DefaultSteamPathMarkers,
		list:     listProcesses,
		killPID:  killProcess,
		sleep:    time.Sleep,
		settle:   settleInterval,
		attempts: settleAttempts,
	}
}

// Run scans running processes and kills every one that matches either a
// configured name (exact, case-insensitive) or a Steam bundle path. It
// then re-scans; any match still present is recorded as a survivor.
func (k *Killer) Run() (Outcome, error) {
	procs, err := k.list()
	if err != nil {
		return Outcome{}, fmt.Errorf("enumerate processes: %w", err)
	}
	want := lowerSet(k.names)

	var out Outcome
	out.Scanned = len(procs)
	failedPIDs := make(map[int]bool)
	matchedAny := false
	for _, p := range procs {
		if !k.matches(p, want) {
			continue
		}
		matchedAny = true
		if err := k.killPID(p.PID); err != nil {
			out.Failed = append(out.Failed, fmt.Sprintf("%d: %v", p.PID, err))
			failedPIDs[p.PID] = true
			continue
		}
		out.KilledPIDs = append(out.KilledPIDs, p.PID)
	}
	sort.Ints(out.KilledPIDs)

	// Honest-verdict re-scan: if anything matched, confirm it is actually
	// gone. Survivors (or an unrunnable re-scan) stop a clean "ok" from
	// masking a live Steam — the green-over-dead-protection class.
	if matchedAny {
		out.Survivors, out.RescanError = k.confirmGone(want, failedPIDs)
	}
	return out, nil
}

// confirmGone polls (bounded) after the kill pass until no Steam match
// remains, giving SIGKILLed processes a moment to be reaped so a successful
// kill is not miscounted as a survivor. A match that persists through the
// whole window is a real survivor — the kill did not take or Steam
// relaunched. PIDs already recorded as Failed are excluded (surfaced there),
// so survivors and failures stay disjoint. If the re-scan ITSELF cannot run,
// it returns a non-empty error string: verification could not confirm the
// system is clean, and the caller must treat that as NOT ok — never a silent
// green over an unverified pass.
func (k *Killer) confirmGone(want map[string]struct{}, failed map[int]bool) ([]int, string) {
	var survivors []int
	for attempt := 0; attempt < k.attempts; attempt++ {
		k.sleep(k.settle)
		after, err := k.list()
		if err != nil {
			return nil, fmt.Sprintf("post-kill re-scan failed: %v", err)
		}
		survivors = nil
		for _, p := range after {
			if failed[p.PID] {
				continue
			}
			if k.matches(p, want) {
				survivors = append(survivors, p.PID)
			}
		}
		if len(survivors) == 0 {
			return nil, "" // confirmed gone
		}
	}
	sort.Ints(survivors)
	return survivors, ""
}

// matches reports whether p is a target: an exact (case-insensitive) comm
// name match, or an executable running from under the Steam bundle.
func (k *Killer) matches(p procView, want map[string]struct{}) bool {
	if _, hit := want[strings.ToLower(p.Name)]; hit {
		return true
	}
	if p.Path == "" {
		return false
	}
	lp := strings.ToLower(p.Path)
	for _, m := range k.markers {
		if strings.Contains(lp, m) {
			return true
		}
	}
	return false
}

func lowerSet(names []string) map[string]struct{} {
	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		want[strings.ToLower(n)] = struct{}{}
	}
	return want
}

func listProcesses() ([]procView, error) {
	ps, err := process.Processes()
	if err != nil {
		return nil, err
	}
	out := make([]procView, 0, len(ps))
	for _, p := range ps {
		// Read BOTH fields best-effort. Name() alone is not enough: Steam's
		// helpers run under generic comm names and are caught by executable
		// path, so a PID whose Name() is unreadable but whose Exe() resolves
		// must still be enumerated — otherwise a path-only match never sees
		// it. Skip only when NEITHER field is readable (nothing to match on).
		name, nameErr := p.Name()
		path, exeErr := p.Exe()
		if nameErr != nil && exeErr != nil {
			continue // process vanished or fully opaque; nothing to match
		}
		out = append(out, procView{PID: int(p.Pid), Name: name, Path: path})
	}
	return out, nil
}

func killProcess(pid int) error {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return err
	}
	return p.Kill()
}
