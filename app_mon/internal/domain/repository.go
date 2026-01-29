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

	// NeedsUpdate checks if plist exists but has different content than expected.
	NeedsUpdate(execPath string) bool

	// Update unloads, updates plist content, and reloads.
	Update(execPath string) error

	// CleanupOtherMode removes plist from the other mode location if exists.
	CleanupOtherMode() error
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

// KeyProvider abstracts the source of encryption keys.
// Phase 1: file-based key. Phase 2: server-generated key.
type KeyProvider interface {
	// GetKey returns the encryption key bytes.
	GetKey() ([]byte, error)

	// StoreKey persists a new encryption key.
	StoreKey(key []byte) error

	// KeyExists checks if a key has been generated.
	KeyExists() bool
}

// SecretStore provides encrypted persistent storage for secrets.
// Secrets are generated once on install and persist across restarts.
// Phase 2: secrets can be synced/rebuilt from server.
type SecretStore interface {
	// GetSecret retrieves a secret by key.
	GetSecret(key string) (string, error)

	// SetSecret stores a secret.
	SetSecret(key, value string) error

	// GetAllSecrets returns all stored secrets.
	GetAllSecrets() (map[string]string, error)

	// Close releases resources (e.g., database connection).
	Close() error
}

// FreedomHealth represents the current health status of Freedom app protection.
type FreedomHealth struct {
	// Installed indicates if Freedom.app exists at expected path
	Installed bool
	// AppRunning indicates if Freedom main process is running
	AppRunning bool
	// ProxyRunning indicates if FreedomProxy process is running
	ProxyRunning bool
	// HelperRunning indicates if com.80pct.FreedomHelper is running
	HelperRunning bool
	// LoginItemPresent indicates if Freedom is in Login Items
	LoginItemPresent bool
	// ProxyPort is the port FreedomProxy listens on (7769)
	ProxyPort int
}

// FreedomProtector monitors and protects Freedom app.
// It ensures Freedom stays running and Login Items are preserved.
type FreedomProtector interface {
	// IsInstalled checks if Freedom.app exists at /Applications/Freedom.app
	IsInstalled() bool

	// IsAppRunning checks if Freedom main process is running
	IsAppRunning() bool

	// IsProxyRunning checks if FreedomProxy process is running
	IsProxyRunning() bool

	// IsHelperRunning checks if com.80pct.FreedomHelper is running
	IsHelperRunning() bool

	// IsLoginItemPresent checks if Freedom is in Login Items
	IsLoginItemPresent() bool

	// RestartApp launches Freedom.app using `open -a`
	RestartApp() error

	// RestoreLoginItem adds Freedom back to Login Items using AppleScript
	RestoreLoginItem() error

	// GetHealth returns comprehensive health status
	GetHealth() FreedomHealth

	// Protect runs a protection cycle: restart app if needed, restore login item if needed
	// Returns true if any action was taken
	Protect() (actionTaken bool, err error)
}
