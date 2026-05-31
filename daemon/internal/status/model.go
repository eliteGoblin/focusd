package status

import "github.com/eliteGoblin/focusd/daemon/internal/status/redact"

// Verdict is the overall (and per-line) health classification.
type Verdict string

const (
	Healthy     Verdict = "HEALTHY"
	Degraded    Verdict = "DEGRADED"
	Down        Verdict = "DOWN"
	Unavailable Verdict = "UNAVAILABLE" // admin-level item under a user install
	Unknown     Verdict = "UNKNOWN"     // could not determine (e.g. needs sudo)
)

// ExitCode maps an overall verdict to the process exit code (FEATURE 09
// acceptance #4). Internal errors are handled separately (code 3).
func (v Verdict) ExitCode() int {
	switch v {
	case Healthy:
		return 0
	case Down:
		return 2
	default: // Degraded / Unknown / Unavailable as overall → degraded
		return 1
	}
}

// AgeBucket is a coarse recency classification (FEATURE 09: precise
// timestamps add no operator value and risk fingerprinting).
type AgeBucket string

const (
	AgeUnder1m AgeBucket = "<1m"
	AgeUnder5m AgeBucket = "<5m"
	AgeUnder1h AgeBucket = "<1h"
	AgeOver1h  AgeBucket = ">1h"
	AgeNever   AgeBucket = "never"
)

// MeshHealth is the engine (launchd mesh) probe result.
type MeshHealth struct {
	RolesRunning int     `json:"roles_running"` // 0..3
	RolesTotal   int     `json:"roles_total"`   // 3
	Known        bool    `json:"known"`         // false ⇒ couldn't determine (needs sudo)
	Verdict      Verdict `json:"verdict"`
}

// PlatformHealth is the platform-version probe result. Only version
// STRINGS are carried — never a path.
type PlatformHealth struct {
	Desired      string  `json:"desired"`       // from version.json
	Good         string  `json:"good"`          // last-known-good
	RunningProcs int     `json:"running_procs"` // live platform processes
	Verdict      Verdict `json:"verdict"`
}

// JobHealth is one plugin/job's last-run summary.
type JobHealth struct {
	JobID   string    `json:"job_id"`
	Status  string    `json:"status"`  // last terminal status (or "none")
	Age     AgeBucket `json:"age"`     // how recently it ran
	Verdict Verdict   `json:"verdict"` // mapped health for this job
}

// HostsHealth is the /etc/hosts blocklist probe result.
type HostsHealth struct {
	MarkersPresent bool    `json:"markers_present"` // BEGIN/END block found
	BlockedCount   int     `json:"blocked_count"`   // 0.0.0.0 lines in the block
	Verdict        Verdict `json:"verdict"`
}

// PfHealth is the pf-table probe result. The anchor name is a Token so it
// can NEVER reach rendered output.
type PfHealth struct {
	Anchor  redact.Token `json:"anchor"` // always renders "<redacted>"
	Entries int          `json:"entries"`
	Known   bool         `json:"known"`   // false ⇒ couldn't determine (needs sudo)
	Enabled bool         `json:"enabled"` // job configured/enabled at all
	Verdict Verdict      `json:"verdict"`
}

// SkillHealth is the Claude skill-files probe result.
type SkillHealth struct {
	Present int     `json:"present"` // 0..3 of the canonical skill artifacts
	Total   int     `json:"total"`   // 3
	Verdict Verdict `json:"verdict"`
}

// EngineView is one discovered install (user or system) plus a Token for
// its workdir (never rendered). Most installs are single-engine (ADR-0010)
// so usually exactly one of these is populated.
type EngineView struct {
	Mode    string       `json:"mode"`    // "user" | "system"
	Workdir redact.Token `json:"workdir"` // always renders "<redacted>"
	Found   bool         `json:"found"`   // a genuine install was discovered here
}

// Snapshot is the whole rendered health read. It carries primitives and
// Tokens only; every Token field renders as "<redacted>" in text and JSON.
type Snapshot struct {
	Engine   EngineView     `json:"engine"`
	Mesh     MeshHealth     `json:"mesh"`
	Platform PlatformHealth `json:"platform"`
	Jobs     []JobHealth    `json:"jobs"`
	Hosts    HostsHealth    `json:"hosts"`
	Pf       PfHealth       `json:"pf"`
	Skills   SkillHealth    `json:"skills"`

	// Mode is the install mode being reported ("user"|"system"). Under a
	// user install the admin-level protections are Unavailable, not Down.
	Mode string `json:"mode"`

	// WarmingUp marks a fresh install (<10m, no runs) — HEALTHY, not
	// DEGRADED (FEATURE 09 acceptance #6).
	WarmingUp bool `json:"warming_up"`

	// Overall is the aggregate verdict; ExitCode derives from it.
	Overall Verdict `json:"overall"`
	// Note is a short human reason attached to the overall verdict (e.g.
	// "warming up", "reinstall with sudo for full coverage"). Never a token.
	Note string `json:"note,omitempty"`
}
