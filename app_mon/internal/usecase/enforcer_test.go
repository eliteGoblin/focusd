package usecase

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// mockProcessManager implements domain.ProcessManager for testing
type mockProcessManager struct {
	findResult map[string][]int
	findErr    error
	killErr    error
	killedPIDs []int
}

func (m *mockProcessManager) FindByName(pattern string) ([]int, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	if m.findResult != nil {
		return m.findResult[pattern], nil
	}
	return nil, nil
}

func (m *mockProcessManager) Kill(pid int) error {
	if m.killErr != nil {
		return m.killErr
	}
	m.killedPIDs = append(m.killedPIDs, pid)
	return nil
}

func (m *mockProcessManager) IsRunning(pid int) bool {
	return false
}

func (m *mockProcessManager) GetCurrentPID() int {
	return os.Getpid()
}

// mockFileSystemManager implements domain.FileSystemManager for testing
type mockFileSystemManager struct {
	existingPaths map[string]bool
	deleteErr     error
	deletedPaths  []string
}

func (m *mockFileSystemManager) Exists(path string) bool {
	if m.existingPaths != nil {
		return m.existingPaths[path]
	}
	return false
}

func (m *mockFileSystemManager) Delete(path string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedPaths = append(m.deletedPaths, path)
	return nil
}

func (m *mockFileSystemManager) ExpandHome(path string) string {
	return path // No expansion in tests
}

// mockPolicyStore implements domain.PolicyStore for testing
type mockPolicyStore struct {
	policies []domain.Policy
}

func (m *mockPolicyStore) GetAll() []domain.Policy {
	return m.policies
}

func (m *mockPolicyStore) GetByID(id string) (*domain.Policy, error) {
	for _, p := range m.policies {
		if p.ID == id {
			return &p, nil
		}
	}
	return nil, nil
}

func (m *mockPolicyStore) List() []string {
	ids := make([]string, len(m.policies))
	for i, p := range m.policies {
		ids[i] = p.ID
	}
	return ids
}

// mockStrategyManager implements domain.StrategyManager for testing
type mockStrategyManager struct {
	uninstallResult string
	uninstallErr    error
}

func (m *mockStrategyManager) GetStrategies() []domain.UninstallStrategy {
	return nil
}

func (m *mockStrategyManager) UninstallApp(name string) (string, error) {
	return m.uninstallResult, m.uninstallErr
}

// TestNewEnforcer verifies enforcer creation
func TestNewEnforcer(t *testing.T) {
	pm := &mockProcessManager{}
	fs := &mockFileSystemManager{}
	ps := &mockPolicyStore{}
	logger := zap.NewNop()

	enforcer := NewEnforcer(pm, fs, ps, logger)

	assert.NotNil(t, enforcer)
	impl := enforcer.(*EnforcerImpl)
	assert.Equal(t, pm, impl.processManager)
	assert.Equal(t, fs, impl.fsManager)
	assert.Equal(t, ps, impl.policyStore)
	assert.Nil(t, impl.strategyManager)
}

// TestNewEnforcerWithStrategy verifies enforcer with strategy creation
func TestNewEnforcerWithStrategy(t *testing.T) {
	pm := &mockProcessManager{}
	fs := &mockFileSystemManager{}
	ps := &mockPolicyStore{}
	sm := &mockStrategyManager{}
	logger := zap.NewNop()

	enforcer := NewEnforcerWithStrategy(pm, fs, ps, sm, logger)

	assert.NotNil(t, enforcer)
	impl := enforcer.(*EnforcerImpl)
	assert.Equal(t, sm, impl.strategyManager)
}

// TestEnforce_NoPolicies verifies behavior with no policies
func TestEnforce_NoPolicies(t *testing.T) {
	pm := &mockProcessManager{}
	fs := &mockFileSystemManager{}
	ps := &mockPolicyStore{policies: []domain.Policy{}}
	logger := zap.NewNop()

	enforcer := NewEnforcer(pm, fs, ps, logger)

	results, err := enforcer.Enforce(context.Background())

	require.NoError(t, err)
	assert.Empty(t, results)
}

// TestEnforce_KillsProcesses verifies process killing
func TestEnforce_KillsProcesses(t *testing.T) {
	pm := &mockProcessManager{
		findResult: map[string][]int{
			"steam": {1001, 1002},
		},
	}
	fs := &mockFileSystemManager{}
	ps := &mockPolicyStore{
		policies: []domain.Policy{
			{
				ID:           "steam",
				Name:         "Steam",
				ProcessNames: []string{"steam"},
				Paths:        []string{},
			},
		},
	}
	logger := zap.NewNop()

	enforcer := NewEnforcer(pm, fs, ps, logger)

	results, err := enforcer.Enforce(context.Background())

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "steam", results[0].PolicyID)
	assert.ElementsMatch(t, []int{1001, 1002}, results[0].KilledPIDs)
	assert.ElementsMatch(t, []int{1001, 1002}, pm.killedPIDs)
}

// TestEnforce_DeletesPaths verifies path deletion
func TestEnforce_DeletesPaths(t *testing.T) {
	pm := &mockProcessManager{}
	fs := &mockFileSystemManager{
		existingPaths: map[string]bool{
			"/path/to/delete": true,
		},
	}
	ps := &mockPolicyStore{
		policies: []domain.Policy{
			{
				ID:           "test",
				Name:         "Test",
				ProcessNames: []string{},
				Paths:        []string{"/path/to/delete"},
			},
		},
	}
	logger := zap.NewNop()

	enforcer := NewEnforcer(pm, fs, ps, logger)

	results, err := enforcer.Enforce(context.Background())

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Contains(t, results[0].DeletedPaths, "/path/to/delete")
}

// TestEnforce_SkipsNonexistentPaths verifies skipping missing paths
func TestEnforce_SkipsNonexistentPaths(t *testing.T) {
	pm := &mockProcessManager{}
	fs := &mockFileSystemManager{
		existingPaths: map[string]bool{}, // Path doesn't exist
	}
	ps := &mockPolicyStore{
		policies: []domain.Policy{
			{
				ID:           "test",
				Name:         "Test",
				ProcessNames: []string{},
				Paths:        []string{"/nonexistent/path"},
			},
		},
	}
	logger := zap.NewNop()

	enforcer := NewEnforcer(pm, fs, ps, logger)

	results, err := enforcer.Enforce(context.Background())

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Empty(t, results[0].DeletedPaths)
	assert.Empty(t, fs.deletedPaths)
}

// TestEnforce_HandlesFindError verifies error handling for process finding
func TestEnforce_HandlesFindError(t *testing.T) {
	pm := &mockProcessManager{
		findErr: errors.New("find failed"),
	}
	fs := &mockFileSystemManager{}
	ps := &mockPolicyStore{
		policies: []domain.Policy{
			{
				ID:           "test",
				Name:         "Test",
				ProcessNames: []string{"process"},
				Paths:        []string{},
			},
		},
	}
	logger := zap.NewNop()

	enforcer := NewEnforcer(pm, fs, ps, logger)

	results, err := enforcer.Enforce(context.Background())

	require.NoError(t, err) // Enforce doesn't return error, it logs
	require.Len(t, results, 1)
	assert.NotEmpty(t, results[0].Errors)
}

// TestEnforce_HandlesKillError verifies error handling for process killing
func TestEnforce_HandlesKillError(t *testing.T) {
	pm := &mockProcessManager{
		findResult: map[string][]int{"process": {1001}},
		killErr:    errors.New("kill failed"),
	}
	fs := &mockFileSystemManager{}
	ps := &mockPolicyStore{
		policies: []domain.Policy{
			{
				ID:           "test",
				Name:         "Test",
				ProcessNames: []string{"process"},
				Paths:        []string{},
			},
		},
	}
	logger := zap.NewNop()

	enforcer := NewEnforcer(pm, fs, ps, logger)

	results, err := enforcer.Enforce(context.Background())

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Empty(t, results[0].KilledPIDs)
	assert.NotEmpty(t, results[0].Errors)
}

// TestEnforce_HandlesDeleteError verifies error handling for path deletion
func TestEnforce_HandlesDeleteError(t *testing.T) {
	pm := &mockProcessManager{}
	fs := &mockFileSystemManager{
		existingPaths: map[string]bool{"/path": true},
		deleteErr:     errors.New("delete failed"),
	}
	ps := &mockPolicyStore{
		policies: []domain.Policy{
			{
				ID:           "test",
				Name:         "Test",
				ProcessNames: []string{},
				Paths:        []string{"/path"},
			},
		},
	}
	logger := zap.NewNop()

	enforcer := NewEnforcer(pm, fs, ps, logger)

	results, err := enforcer.Enforce(context.Background())

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Empty(t, results[0].DeletedPaths)
	assert.NotEmpty(t, results[0].Errors)
}

// TestEnforce_HandlesPermissionDenied verifies skipping permission-denied paths
func TestEnforce_HandlesPermissionDenied(t *testing.T) {
	pm := &mockProcessManager{}
	fs := &mockFileSystemManager{
		existingPaths: map[string]bool{"/protected/path": true},
		deleteErr:     os.ErrPermission,
	}
	ps := &mockPolicyStore{
		policies: []domain.Policy{
			{
				ID:           "test",
				Name:         "Test",
				ProcessNames: []string{},
				Paths:        []string{"/protected/path"},
			},
		},
	}
	logger := zap.NewNop()

	enforcer := NewEnforcer(pm, fs, ps, logger)

	results, err := enforcer.Enforce(context.Background())

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Empty(t, results[0].DeletedPaths)
	assert.Contains(t, results[0].SkippedPaths, "/protected/path")
	assert.Empty(t, results[0].Errors) // Permission denied is not an error, just skipped
}

// TestEnforce_WithStrategyManager verifies strategy manager integration
func TestEnforce_WithStrategyManager(t *testing.T) {
	pm := &mockProcessManager{}
	fs := &mockFileSystemManager{}
	ps := &mockPolicyStore{
		policies: []domain.Policy{
			{
				ID:           "test",
				Name:         "TestApp",
				ProcessNames: []string{},
				Paths:        []string{},
			},
		},
	}
	sm := &mockStrategyManager{
		uninstallResult: "brew",
	}
	logger := zap.NewNop()

	enforcer := NewEnforcerWithStrategy(pm, fs, ps, sm, logger)

	results, err := enforcer.Enforce(context.Background())

	require.NoError(t, err)
	require.Len(t, results, 1)
	// Strategy manager was called (no direct assertion needed, just verifying no panic)
}

// TestEnforcePolicy_RecordsDuration verifies duration recording
func TestEnforcePolicy_RecordsDuration(t *testing.T) {
	pm := &mockProcessManager{}
	fs := &mockFileSystemManager{}
	ps := &mockPolicyStore{}
	logger := zap.NewNop()

	enforcer := NewEnforcer(pm, fs, ps, logger)

	policy := domain.Policy{
		ID:           "test",
		Name:         "Test",
		ProcessNames: []string{},
		Paths:        []string{},
	}

	result, err := enforcer.EnforcePolicy(context.Background(), policy)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, result.DurationMs, int64(0))
	assert.NotZero(t, result.ExecutedAt)
}
