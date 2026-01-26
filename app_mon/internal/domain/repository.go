package domain

import "context"

// ProcessManager handles OS process operations.
// Implementation: uses gopsutil for cross-platform support.
type ProcessManager interface {
	// FindByName returns PIDs of processes matching the pattern.
	FindByName(pattern string) ([]int, error)

	// Kill terminates a process by PID (SIGKILL).
	Kill(pid int) error

	// IsRunning checks if a PID exists and is running.
	IsRunning(pid int) bool

	// GetCurrentPID returns the current process PID.
	GetCurrentPID() int
}

// FileSystemManager handles filesystem operations.
type FileSystemManager interface {
	// Exists checks if a path exists.
	Exists(path string) bool

	// Delete removes a file or directory recursively.
	Delete(path string) error

	// ExpandHome expands ~ to the user's home directory.
	ExpandHome(path string) string
}

// DaemonRegistry provides daemon discovery and registration.
// Daemons find each other via PID stored in a hidden registry file.
// Implementation: hidden JSON file in /var/tmp/
type DaemonRegistry interface {
	// Register saves current daemon's PID and obfuscated name.
	Register(daemon Daemon) error

	// GetPartner returns the partner daemon info (watcher<->guardian).
	GetPartner(role DaemonRole) (*Daemon, error)

	// UpdateHeartbeat updates timestamp for liveness check.
	UpdateHeartbeat(role DaemonRole) error

	// IsPartnerAlive checks if partner daemon is running via PID.
	IsPartnerAlive(role DaemonRole) (bool, error)

	// GetAll returns full registry state (for status command).
	GetAll() (*RegistryEntry, error)

	// Clear removes registry file (for clean restart).
	Clear() error

	// GetRegistryPath returns the hidden registry file path (for tests).
	GetRegistryPath() string
}

// PolicyStore provides access to app blocking policies.
// Implementation: hardcoded for MVP (future: Azure Cosmos DB).
type PolicyStore interface {
	// GetAll returns all registered policies.
	GetAll() []Policy

	// GetByID returns policy for specific app.
	GetByID(id string) (*Policy, error)

	// List returns app IDs of all blocked apps.
	List() []string
}

// Enforcer orchestrates process killing and file deletion.
type Enforcer interface {
	// Enforce runs all policies once.
	Enforce(ctx context.Context) ([]EnforcementResult, error)

	// EnforcePolicy runs a single policy.
	EnforcePolicy(ctx context.Context, policy Policy) (*EnforcementResult, error)
}

// LaunchAgentManager handles macOS LaunchAgent plist operations.
type LaunchAgentManager interface {
	// Install creates and loads the LaunchAgent plist.
	Install(execPath string) error

	// Uninstall unloads and removes the LaunchAgent plist.
	Uninstall() error

	// IsInstalled checks if LaunchAgent is installed.
	IsInstalled() bool

	// GetPlistPath returns the plist file path.
	GetPlistPath() string
}

// Obfuscator generates system-looking process names.
type Obfuscator interface {
	// GenerateName creates a random system-looking process name.
	// Example: "com.apple.cfprefsd.xpc.a1b2c3"
	GenerateName() string
}

// UninstallStrategy defines how to uninstall an app via a specific method.
// Implementations: brew (macOS), apt/snap/flatpak (Linux), winget (Windows)
type UninstallStrategy interface {
	// Name returns the strategy name (e.g., "brew", "apt", "snap")
	Name() string

	// IsAvailable returns true if this strategy can be used on this system
	IsAvailable() bool

	// IsInstalled checks if the package is installed via this method
	IsInstalled(pkg string) bool

	// Uninstall removes the package
	Uninstall(pkg string) error
}

// StrategyManager discovers and manages available uninstall strategies.
type StrategyManager interface {
	// GetStrategies returns all available strategies for this platform
	GetStrategies() []UninstallStrategy

	// UninstallApp tries to uninstall using all available strategies
	// Returns the strategy name that succeeded, or empty if none
	UninstallApp(appName string) (string, error)
}
