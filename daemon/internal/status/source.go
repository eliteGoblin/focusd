// Package status implements `daemon status`: a read-only health snapshot
// of the focusd install that, by construction, never prints the disguised
// identifiers (workdir, launchd labels, daemon binary name, pf anchor).
//
// The package is split into a pure core (probes + aggregation + render)
// and a thin OS-bound Source. Probes take a Source and return small
// structs carrying primitives plus redact.Tokens; the renderer only ever
// prints primitives (and Tokens, which render as "<redacted>"). This keeps
// every probe unit-testable against a fake Source and makes the redaction
// contract (ADR-0011) a property of the types, not the renderer.
package status

import "time"

// Source is the OS seam every probe reads through: launchctl, the process
// table, the filesystem, and the state DB. Production wires a real
// implementation (os/exec + os + sqlite); tests pass a fake so probes run
// with zero side effects and deterministic inputs.
//
// Every method returns (value, error). A nil error with a sentinel
// "unknown" value (e.g. ok=false, available=false) means the probe could
// not determine the answer (typically: needs root and we don't have it) —
// distinct from a hard error, which means the Source itself failed.
type Source interface {
	// Geteuid reports the effective uid so probes can tell a privileged
	// query (sudo) from an unprivileged one without re-resolving mode.
	Geteuid() int

	// LaunchctlLoaded reports whether a launchd label is registered in the
	// given domain. ok=false means "could not determine" (e.g. system
	// domain queried without root) — NOT "definitely absent".
	LaunchctlLoaded(domain, label string) (loaded, ok bool)

	// ReadFile reads an arbitrary file (e.g. /etc/hosts, version.json).
	ReadFile(path string) ([]byte, error)

	// FileExists reports whether path exists (any type).
	FileExists(path string) bool

	// CountProcesses returns how many running processes have execPath as
	// their executable path. Used for the live-platform-process count.
	CountProcesses(execPath string) (n int, err error)

	// PfTableCount returns the number of entries in the pf table inside the
	// given anchor. ok=false means "could not determine" (needs root /
	// pfctl unavailable). Anchor + table are raw strings here because the
	// Source is the privileged boundary; callers pass them via redact.Use
	// so the raw value never escapes into rendered output.
	PfTableCount(anchor, table string) (n int, ok bool)

	// LastJobRun returns the most-recent run for a job from state.db: its
	// terminal status and when it started. found=false means no run row
	// exists yet (fresh install / never ran). A DB-open failure is err.
	LastJobRun(dbPath, jobID string) (run JobRunInfo, found bool, err error)

	// Now is the clock probes use for age/recency math (injectable for
	// deterministic tests).
	Now() time.Time
}

// JobRunInfo is the minimal projection of a state.db job_runs row that the
// plugins probe needs: the terminal status and the start time. It carries
// no disguised identifiers.
type JobRunInfo struct {
	Status    string    // ok|failed|error|timedout|skipped|unavailable|running
	StartedAt time.Time // parsed from the RFC3339Nano started_at column
}
