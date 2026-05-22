// Package osadapter is the single boundary between the OS-agnostic core
// platform and OS-specific lifecycle, privilege, and path concerns.
//
// The core MUST NOT contain direct macOS/Windows logic. It calls
// osadapter.NewAdapter() and depends only on the Adapter interface.
// OS-specific implementations live in build-tagged files
// (adapter_darwin.go / adapter_windows.go / adapter_linux.go).
//
// Per the refactor spec the first implementation is intentionally small:
// identity, run-mode detection, and the default path layout. Agent
// lifecycle (Install/Start/Stop) is declared here but implemented in a
// later phase; calling it now returns ErrNotImplemented.
package osadapter

import "errors"

// RunMode is the privilege context the platform runs under.
type RunMode string

const (
	// ModeUser runs as the logged-in user; no admin/root. Suitable for
	// managed laptops without admin access.
	ModeUser RunMode = "user"
	// ModeSystem runs as root/admin/system; stronger protection mode.
	ModeSystem RunMode = "system"
)

// Valid reports whether m is a known run mode.
func (m RunMode) Valid() bool { return m == ModeUser || m == ModeSystem }

// ErrNotImplemented is returned by adapter methods that are declared for
// forward compatibility but not yet wired for the current OS/phase.
var ErrNotImplemented = errors.New("osadapter: not implemented for this OS/phase")

// Adapter abstracts every OS-specific concern the core needs. Keep it
// small; do not push protection-job logic in here.
type Adapter interface {
	// Identity ---------------------------------------------------------
	Name() string        // human label, e.g. "darwin"
	CurrentOS() string   // GOOS
	CurrentArch() string // GOARCH

	// Run mode ---------------------------------------------------------
	// DetectRunMode reports the privilege context inferred from the
	// running process (root => system, otherwise user).
	DetectRunMode() RunMode
	CanRunAsUser() bool
	CanRunAsSystem() bool

	// Default path layout (per spec §Suggested app runtime folder layout).
	// All paths are mode-aware: user vs system live in different roots so
	// the two modes are completely isolated.
	DefaultBaseDir(mode RunMode) (string, error)
	DefaultConfigPath(mode RunMode) (string, error) // <base>/config.yaml
	DefaultPluginDir(mode RunMode) (string, error)  // <base>/plugins
	DefaultLogDir(mode RunMode) (string, error)     // <base>/logs
	DefaultStateDir(mode RunMode) (string, error)   // <base>/state

	// Agent lifecycle (implemented in a later phase). Declared now so the
	// core depends on a stable interface.
	InstallAgent(mode RunMode) error
	UninstallAgent(mode RunMode) error
	IsAgentInstalled(mode RunMode) (bool, error)
	StartAgent(mode RunMode) error
	StopAgent(mode RunMode) error
	IsAgentRunning(mode RunMode) (bool, error)
}
