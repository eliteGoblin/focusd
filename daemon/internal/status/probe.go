package status

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/status/redact"
)

// ProbeInput is everything the pure probes need to read one install's
// health. Disguised values arrive as redact.Tokens (labels, workdir, pf
// anchor) so they can be USED to query the Source but never rendered.
type ProbeInput struct {
	Mode   string // "user" | "system"
	Domain string // launchctl domain target (NOT disguised: "system" / "gui/<uid>")

	// MeshLabels are the three disguised launchd labels (base.a/.b/.ensure).
	// Empty slice ⇒ no install discovered.
	MeshLabels []redact.Token

	Workdir     redact.Token // disguised workdir; Use it to locate version.json/state.db
	HostsPath   string       // /etc/hosts (not disguised)
	SteamFamily []string     // host substrings that mark a present blocklist

	PfAnchor  redact.Token // disguised pf anchor name; never rendered
	PfTable   string       // table name (not a disguised identifier)
	PfEnabled bool         // is the network-block job configured/enabled

	// SkillPaths are the three canonical ~/.claude artifacts (not disguised).
	SkillPaths []string

	// PlatformProcPath is the live platform binary path to count processes
	// for. Empty ⇒ skip the process count (report 0 known-unknown).
	PlatformProcPath redact.Token

	// Jobs is the ordered list of (jobID, adminLevel) the plugins probe
	// reports. adminLevel jobs are Unavailable under a user install.
	Jobs []JobSpec
}

// JobSpec names a job to probe and whether it is an admin-level protection
// (Unavailable under a user-mode install rather than Down/failed).
type JobSpec struct {
	JobID      string
	AdminLevel bool
}

// ProbeMesh counts how many mesh roles are loaded in launchd. System-mode
// labels need root to query; without it the result is Known=false (the
// status line reads "unknown (re-run with sudo)").
func ProbeMesh(src Source, in ProbeInput) MeshHealth {
	h := MeshHealth{RolesTotal: 3}
	if len(in.MeshLabels) == 0 {
		// No install discovered at all → engine is down (0 of 3).
		h.Known = true
		h.Verdict = Down
		return h
	}
	anyUnknown := false
	for _, lbl := range in.MeshLabels {
		loaded, ok := redact.Use(lbl, func(raw string) boolPair {
			l, k := src.LaunchctlLoaded(in.Domain, raw)
			return boolPair{a: l, b: k}
		}).unpack()
		if !ok {
			anyUnknown = true
			continue
		}
		if loaded {
			h.RolesRunning++
		}
	}
	if anyUnknown && h.RolesRunning == 0 {
		// Couldn't determine anything (e.g. system domain, no sudo).
		h.Known = false
		h.Verdict = Unknown
		return h
	}
	h.Known = true
	switch {
	case h.RolesRunning == 0:
		h.Verdict = Down
	case h.RolesRunning < h.RolesTotal:
		h.Verdict = Degraded
	default:
		h.Verdict = Healthy
	}
	return h
}

// boolPair lets redact.Use return two values through its single generic T.
type boolPair struct{ a, b bool }

func (p boolPair) unpack() (bool, bool) { return p.a, p.b }

// ProbePlatform reads desired (version.json) + good + live process count.
// Only version strings cross the boundary; the workdir path stays a Token.
func ProbePlatform(src Source, in ProbeInput) PlatformHealth {
	h := PlatformHealth{}
	if in.Workdir.Present() {
		desired := redact.Use(in.Workdir, func(wd string) string {
			b, err := src.ReadFile(wd + "/version.json")
			if err != nil {
				return ""
			}
			var c struct {
				Desired string `json:"desired"`
			}
			if json.Unmarshal(b, &c) != nil {
				return ""
			}
			return c.Desired
		})
		good := redact.Use(in.Workdir, func(wd string) string {
			b, err := src.ReadFile(wd + "/good")
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(b))
		})
		h.Desired = desired
		h.Good = good
	}
	if in.PlatformProcPath.Present() {
		n := redact.Use(in.PlatformProcPath, func(p string) int {
			n, _ := src.CountProcesses(p)
			return n
		})
		h.RunningProcs = n
	}
	switch {
	case h.RunningProcs == 0:
		h.Verdict = Down
	case h.Desired != "" && h.Good != "" && h.Desired != h.Good:
		h.Verdict = Degraded // version drift: desired not yet promoted to good
	default:
		h.Verdict = Healthy
	}
	return h
}

// ProbePlugins reads each job's last run from state.db and maps it to a
// JobHealth. Admin-level jobs under a user install report Unavailable.
func ProbePlugins(src Source, in ProbeInput) []JobHealth {
	out := make([]JobHealth, 0, len(in.Jobs))
	dbPath := redact.Use(in.Workdir, func(wd string) string {
		if wd == "" {
			return ""
		}
		return wd + "/state.db"
	})
	for _, j := range in.Jobs {
		jh := JobHealth{JobID: j.JobID}
		if j.AdminLevel && in.Mode == "user" {
			jh.Status = "unavailable"
			jh.Age = AgeNever
			jh.Verdict = Unavailable
			out = append(out, jh)
			continue
		}
		run, found, err := src.LastJobRun(dbPath, j.JobID)
		if err != nil || !found {
			jh.Status = "none"
			jh.Age = AgeNever
			jh.Verdict = Unknown // no run yet — aggregator handles warming-up
			out = append(out, jh)
			continue
		}
		jh.Status = run.Status
		jh.Age = bucketAge(src.Now().Sub(run.StartedAt))
		jh.Verdict = jobVerdict(run.Status, jh.Age)
		out = append(out, jh)
	}
	return out
}

// jobVerdict maps a job's last terminal status + recency to health.
func jobVerdict(status string, age AgeBucket) Verdict {
	switch status {
	case "ok", "skipped":
		if age == AgeOver1h {
			return Degraded // ran fine but not recently → stale
		}
		return Healthy
	case "unavailable":
		return Unavailable
	case "failed", "error", "timedout":
		return Degraded
	default:
		return Unknown
	}
}

// bucketAge classifies an elapsed duration into a coarse recency bucket.
func bucketAge(d time.Duration) AgeBucket {
	switch {
	case d < 0:
		return AgeUnder1m // clock skew → treat as fresh
	case d < time.Minute:
		return AgeUnder1m
	case d < 5*time.Minute:
		return AgeUnder5m
	case d < time.Hour:
		return AgeUnder1h
	default:
		return AgeOver1h
	}
}

// ProbeHosts counts the Steam/Valve block lines in /etc/hosts.
func ProbeHosts(src Source, in ProbeInput) HostsHealth {
	h := HostsHealth{}
	b, err := src.ReadFile(in.HostsPath)
	if err != nil {
		h.Verdict = Down // can't read hosts → blocklist effectively gone
		return h
	}
	for _, line := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "0.0.0.0") {
			continue
		}
		for _, fam := range in.SteamFamily {
			if strings.Contains(l, fam) {
				h.BlockedCount++
				h.MarkersPresent = true
				break
			}
		}
	}
	if h.MarkersPresent {
		h.Verdict = Healthy
	} else {
		h.Verdict = Down // markers absent → first-line block missing
	}
	return h
}

// ProbePf reads the pf table entry count. Needs root; without it Known is
// false and the line reads "unknown (re-run with sudo)".
func ProbePf(src Source, in ProbeInput) PfHealth {
	h := PfHealth{Anchor: in.PfAnchor, Enabled: in.PfEnabled}
	if !in.PfEnabled {
		// network-block is off by default; not a failure.
		h.Known = true
		h.Verdict = Unavailable
		return h
	}
	n, ok := redact.Use(in.PfAnchor, func(anchor string) boolPair {
		c, k := src.PfTableCount(anchor, in.PfTable)
		h.Entries = c
		return boolPair{a: c > 0, b: k}
	}).unpack()
	_ = n
	if !ok {
		h.Known = false
		h.Verdict = Unknown
		return h
	}
	h.Known = true
	if h.Entries > 0 {
		h.Verdict = Healthy
	} else {
		h.Verdict = Degraded // enabled but table empty → not enforcing yet
	}
	return h
}

// ProbeSkills counts how many of the three canonical Claude skill files
// are present on disk.
func ProbeSkills(src Source, in ProbeInput) SkillHealth {
	h := SkillHealth{Total: len(in.SkillPaths)}
	for _, p := range in.SkillPaths {
		if src.FileExists(p) {
			h.Present++
		}
	}
	switch {
	case h.Present == 0:
		h.Verdict = Down // all skill files gone → behavioural layer absent
	case h.Present < h.Total:
		h.Verdict = Degraded
	default:
		h.Verdict = Healthy
	}
	return h
}
