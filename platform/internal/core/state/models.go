package state

import "time"

// Validation status values for a discovered plugin.
const (
	ValidationOK       = "ok"
	ValidationRejected = "rejected"
)

// Job run status values (mapped from plugin exit codes by the runner).
const (
	RunStatusOK       = "ok"       // exit 0
	RunStatusFailed   = "failed"   // exit 1 (controlled failure)
	RunStatusError    = "error"    // exit 2+ / spawn error
	RunStatusTimedOut = "timedout" // killed on timeout
	RunStatusSkipped  = "skipped"  // no-overlap skip
	// RunStatusUnavailable marks a job that COULD NOT run in this install
	// mode, distinct from a failure. Two cases:
	//   - a system plugin under a user-mode platform (no escalation), and
	//   - a current_user plugin under a system platform when no console
	//     user is logged in (would corrupt the user's files as root).
	// It feeds a future `daemon status` ("requires system-mode install"
	// / "waiting for console user") without polluting failure metrics.
	RunStatusUnavailable = "unavailable"
)

// Event severities.
const (
	SeverityInfo  = "info"
	SeverityWarn  = "warn"
	SeverityError = "error"
)

// Plugin is a row of the plugin inventory.
type Plugin struct {
	ID                string
	Name              string
	Version           string
	Type              string
	ProtocolVersion   string
	Entrypoint        string
	Path              string
	SupportedOS       string // comma-joined
	SupportedArch     string // comma-joined
	RequiredPrivilege string
	RunAs             string
	Enabled           bool
	DiscoveredAt      string
	LastSeenAt        string
	ValidationStatus  string
	ValidationError   string
}

// Job is the observed projection of a configured job.
type Job struct {
	ID           string
	PluginID     string
	Enabled      bool
	Schedule     string
	TimeoutMS    int64
	Retry        int
	AllowOverlap bool
	ConfigHash   string
	CreatedAt    string
	UpdatedAt    string
}

// JobRun is one execution record.
type JobRun struct {
	ID            int64
	JobID         string
	PluginID      string
	PluginVersion string
	StartedAt     string
	EndedAt       string
	DurationMS    int64
	Status        string
	ExitCode      int
	Message       string
	StdoutJSON    string
	StderrText    string
	ErrorText     string
	TimedOut      bool
	TriggeredBy   string
}

// nowTime exposes the canonical clock for callers that need a time.Time.
func nowTime() time.Time { return time.Now().UTC() }
