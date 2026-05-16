// Package killer terminates Steam/Dota2 processes. Ported from the
// app_mon v0.6.1 policy + process layer, including the v0.6.1 #17 fix:
// process names are matched EXACTLY (case-insensitive), never as a
// substring — substring matching killed Microsoft Teams via the "steam"
// inside "msteams".
package killer

import (
	"fmt"
	"sort"
	"strings"

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

// Outcome summarises a kill pass.
type Outcome struct {
	Scanned    int      `json:"scanned"`
	KilledPIDs []int    `json:"killed_pids"`
	Failed     []string `json:"failed,omitempty"` // "pid: reason"
}

// KilledCount is the number of processes successfully terminated.
func (o Outcome) KilledCount() int { return len(o.KilledPIDs) }

// procLister/procKiller are seams so tests don't touch real processes.
type procView struct {
	PID  int
	Name string
}

type Killer struct {
	names   []string
	list    func() ([]procView, error)
	killPID func(pid int) error
}

// New builds a Killer. Empty names => DefaultProcessNames.
func New(names []string) *Killer {
	if len(names) == 0 {
		names = DefaultProcessNames
	}
	return &Killer{names: names, list: listProcesses, killPID: killProcess}
}

// Run scans running processes and kills every one whose basename exactly
// (case-insensitively) matches a configured name.
func (k *Killer) Run() (Outcome, error) {
	procs, err := k.list()
	if err != nil {
		return Outcome{}, fmt.Errorf("enumerate processes: %w", err)
	}
	want := make(map[string]struct{}, len(k.names))
	for _, n := range k.names {
		want[strings.ToLower(n)] = struct{}{}
	}

	var out Outcome
	out.Scanned = len(procs)
	for _, p := range procs {
		if _, hit := want[strings.ToLower(p.Name)]; !hit {
			continue
		}
		if err := k.killPID(p.PID); err != nil {
			out.Failed = append(out.Failed, fmt.Sprintf("%d: %v", p.PID, err))
			continue
		}
		out.KilledPIDs = append(out.KilledPIDs, p.PID)
	}
	sort.Ints(out.KilledPIDs)
	return out, nil
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
		out = append(out, procView{PID: int(p.Pid), Name: name})
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
