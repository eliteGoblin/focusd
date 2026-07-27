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

// settleDelay is how long we wait after killing before re-scanning, so a
// SIGKILLed process has a moment to be reaped before we count it as a
// survivor (avoids a false "still alive" on a kill that actually took).
const settleDelay = 500 * time.Millisecond

// Outcome summarises a kill pass.
type Outcome struct {
	Scanned    int      `json:"scanned"`
	KilledPIDs []int    `json:"killed_pids"`
	Failed     []string `json:"failed,omitempty"`    // "pid: reason"
	Survivors  []int    `json:"survivors,omitempty"` // Steam procs still present after the kill
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
	names   []string
	markers []string
	list    func() ([]procView, error)
	killPID func(pid int) error
	sleep   func(time.Duration)
	settle  time.Duration
}

// New builds a Killer. Empty names => DefaultProcessNames.
func New(names []string) *Killer {
	if len(names) == 0 {
		names = DefaultProcessNames
	}
	return &Killer{
		names:   names,
		markers: DefaultSteamPathMarkers,
		list:    listProcesses,
		killPID: killProcess,
		sleep:   time.Sleep,
		settle:  settleDelay,
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
	matchedAny := false
	for _, p := range procs {
		if !k.matches(p, want) {
			continue
		}
		matchedAny = true
		if err := k.killPID(p.PID); err != nil {
			out.Failed = append(out.Failed, fmt.Sprintf("%d: %v", p.PID, err))
			continue
		}
		out.KilledPIDs = append(out.KilledPIDs, p.PID)
	}
	sort.Ints(out.KilledPIDs)

	// Honest-verdict re-scan: if anything matched, let the kills settle and
	// re-enumerate. A process STILL matching is a survivor — the kill did
	// not take, or Steam relaunched. Surfacing survivors stops a clean "ok"
	// from masking a live Steam (the green-over-dead-protection class).
	if matchedAny {
		k.sleep(k.settle)
		if after, err := k.list(); err == nil {
			for _, p := range after {
				if k.matches(p, want) {
					out.Survivors = append(out.Survivors, p.PID)
				}
			}
			sort.Ints(out.Survivors)
		}
	}
	return out, nil
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
		name, err := p.Name()
		if err != nil {
			continue // process vanished or unreadable; skip
		}
		path, _ := p.Exe() // best-effort; empty on EPERM/vanished — name-match still works
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
