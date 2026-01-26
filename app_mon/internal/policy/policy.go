// Package policy implements the Strategy pattern for app-specific blocking rules.
// Each app (Steam, Dota2) has its own policy defining what to kill/delete.
package policy

import (
	"context"
	"time"

	"github.com/user/focusd/app_mon/internal/domain"
)

// DefaultScanInterval is 10 minutes as per user preference (low CPU usage).
const DefaultScanInterval = 10 * time.Minute

// AppPolicy defines the strategy interface for blocking an application.
// Implementations provide app-specific process names and paths.
type AppPolicy interface {
	// ID returns unique identifier (e.g., "steam", "dota2").
	ID() string

	// Name returns human-readable name for display.
	Name() string

	// ProcessPatterns returns process names to kill.
	// Patterns are matched case-insensitively.
	ProcessPatterns() []string

	// PathsToDelete returns filesystem paths to remove.
	// Supports ~ expansion for home directory.
	PathsToDelete() []string

	// ScanInterval returns how often to scan for this app.
	ScanInterval() time.Duration

	// PreEnforce is called before enforcement (optional hook).
	PreEnforce(ctx context.Context) error

	// PostEnforce is called after enforcement (optional hook).
	PostEnforce(ctx context.Context, result *domain.EnforcementResult) error
}

// ToPolicy converts an AppPolicy to a domain.Policy entity.
func ToPolicy(ap AppPolicy) domain.Policy {
	return domain.Policy{
		ID:           ap.ID(),
		Name:         ap.Name(),
		ProcessNames: ap.ProcessPatterns(),
		Paths:        ap.PathsToDelete(),
		ScanInterval: ap.ScanInterval(),
	}
}
