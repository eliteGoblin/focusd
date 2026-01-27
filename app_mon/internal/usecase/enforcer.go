// Package usecase contains application business logic.
package usecase

import (
	"context"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// EnforcerImpl implements domain.Enforcer.
type EnforcerImpl struct {
	processManager  domain.ProcessManager
	fsManager       domain.FileSystemManager
	policyStore     domain.PolicyStore
	strategyManager domain.StrategyManager
	logger          *zap.Logger
}

// NewEnforcer creates a new policy enforcer.
func NewEnforcer(
	pm domain.ProcessManager,
	fs domain.FileSystemManager,
	ps domain.PolicyStore,
	logger *zap.Logger,
) domain.Enforcer {
	return &EnforcerImpl{
		processManager:  pm,
		fsManager:       fs,
		policyStore:     ps,
		strategyManager: nil, // Set via WithStrategyManager
		logger:          logger,
	}
}

// NewEnforcerWithStrategy creates an enforcer with strategy manager for package uninstallation.
func NewEnforcerWithStrategy(
	pm domain.ProcessManager,
	fs domain.FileSystemManager,
	ps domain.PolicyStore,
	sm domain.StrategyManager,
	logger *zap.Logger,
) domain.Enforcer {
	return &EnforcerImpl{
		processManager:  pm,
		fsManager:       fs,
		policyStore:     ps,
		strategyManager: sm,
		logger:          logger,
	}
}

// Enforce runs all policies once.
func (e *EnforcerImpl) Enforce(ctx context.Context) ([]domain.EnforcementResult, error) {
	policies := e.policyStore.GetAll()
	results := make([]domain.EnforcementResult, 0, len(policies))

	for _, policy := range policies {
		result, err := e.EnforcePolicy(ctx, policy)
		if err != nil {
			e.logger.Warn("policy enforcement failed",
				zap.String("policy", policy.ID),
				zap.Error(err))
		}
		if result != nil {
			results = append(results, *result)
		}
	}

	return results, nil
}

// EnforcePolicy runs a single policy.
func (e *EnforcerImpl) EnforcePolicy(ctx context.Context, policy domain.Policy) (*domain.EnforcementResult, error) {
	start := time.Now()

	result := &domain.EnforcementResult{
		PolicyID:     policy.ID,
		KilledPIDs:   make([]int, 0),
		DeletedPaths: make([]string, 0),
		SkippedPaths: make([]string, 0),
		Errors:       make([]error, 0),
		ExecutedAt:   start,
	}

	// Kill matching processes
	for _, pattern := range policy.ProcessNames {
		pids, err := e.processManager.FindByName(pattern)
		if err != nil {
			e.logger.Warn("failed to find processes",
				zap.String("pattern", pattern),
				zap.Error(err))
			result.Errors = append(result.Errors, err)
			continue
		}

		for _, pid := range pids {
			if err := e.processManager.Kill(pid); err != nil {
				e.logger.Warn("failed to kill process",
					zap.Int("pid", pid),
					zap.Error(err))
				result.Errors = append(result.Errors, err)
			} else {
				e.logger.Info("killed process",
					zap.String("policy", policy.ID),
					zap.Int("pid", pid),
					zap.String("pattern", pattern))
				result.KilledPIDs = append(result.KilledPIDs, pid)
			}
		}
	}

	// Try to uninstall via package managers (brew, apt, etc.)
	// This is best-effort; if it fails (e.g., needs sudo), path deletion handles it
	if e.strategyManager != nil {
		strategyName, err := e.strategyManager.UninstallApp(policy.Name)
		if err != nil {
			e.logger.Debug("package manager uninstall skipped",
				zap.String("policy", policy.ID),
				zap.Error(err))
		} else if strategyName != "" {
			e.logger.Info("uninstalled via package manager",
				zap.String("policy", policy.ID),
				zap.String("strategy", strategyName),
				zap.String("package", policy.Name))
		}
	}

	// Delete matching paths
	for _, path := range policy.Paths {
		expanded := e.fsManager.ExpandHome(path)

		if !e.fsManager.Exists(expanded) {
			// Path doesn't exist, skip silently
			continue
		}

		if err := e.fsManager.Delete(path); err != nil {
			// Check if it's a permission error
			if os.IsPermission(err) {
				e.logger.Warn("cannot delete (permission denied, run as root)",
					zap.String("path", expanded),
					zap.String("policy", policy.ID))
				result.SkippedPaths = append(result.SkippedPaths, expanded)
			} else {
				e.logger.Warn("failed to delete path",
					zap.String("path", path),
					zap.Error(err))
				result.Errors = append(result.Errors, err)
			}
		} else {
			e.logger.Info("deleted path",
				zap.String("policy", policy.ID),
				zap.String("path", path))
			result.DeletedPaths = append(result.DeletedPaths, path)
		}
	}

	result.DurationMs = time.Since(start).Milliseconds()

	return result, nil
}

// Ensure EnforcerImpl implements domain.Enforcer.
var _ domain.Enforcer = (*EnforcerImpl)(nil)
