// Package domain contains core business entities and interfaces.
// This is the innermost layer in Clean Architecture - no external dependencies.
package domain

import "time"

// DaemonRole identifies the type of daemon process.
type DaemonRole string

const (
	RoleWatcher  DaemonRole = "watcher"
	RoleGuardian DaemonRole = "guardian"
)

// Daemon represents a running daemon process.
type Daemon struct {
	PID            int
	Role           DaemonRole
	ObfuscatedName string
	StartedAt      time.Time
	AppVersion     string // Version of the app binary
}

// RegistryEntry stores the state of both daemons for mutual discovery.
// Persisted to a hidden file for cross-process communication.
type RegistryEntry struct {
	Version       int    `json:"version"`
	WatcherPID    int    `json:"watcher_pid"`
	WatcherName   string `json:"watcher_name"`
	GuardianPID   int    `json:"guardian_pid"`
	GuardianName  string `json:"guardian_name"`
	LastHeartbeat int64  `json:"last_heartbeat"`
	Mode          string `json:"mode,omitempty"`        // "user" or "system" - for mode switch detection
	AppVersion    string `json:"app_version,omitempty"` // Version of running daemons
}

// Policy defines what an app blocker policy contains.
type Policy struct {
	ID           string
	Name         string
	ProcessNames []string      // Process name patterns to kill
	Paths        []string      // Filesystem paths to delete
	ScanInterval time.Duration // How often to scan (default 10 min)
}

// EnforcementResult captures what happened during a single enforcement run.
type EnforcementResult struct {
	PolicyID     string
	KilledPIDs   []int
	DeletedPaths []string
	SkippedPaths []string // Paths that couldn't be deleted (permission denied)
	Errors       []error
	ExecutedAt   time.Time
	DurationMs   int64
}
