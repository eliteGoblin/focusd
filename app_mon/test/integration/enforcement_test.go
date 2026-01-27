//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
	"github.com/eliteGoblin/focusd/app_mon/internal/infra"
	"github.com/eliteGoblin/focusd/app_mon/internal/policy"
	"github.com/eliteGoblin/focusd/app_mon/internal/usecase"
	"github.com/eliteGoblin/focusd/app_mon/test/fixtures"
)

func TestEnforcer_DeletesSteamPaths(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "appmon-integration-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create fake Steam structure
	fakeSteam := fixtures.NewFakeSteamStructure(tmpDir)
	if err := fakeSteam.Create(); err != nil {
		t.Fatalf("failed to create fake steam: %v", err)
	}

	// Verify it exists
	if !fakeSteam.Exists() {
		t.Fatal("expected fake steam to exist")
	}
	if !fakeSteam.Dota2Exists() {
		t.Fatal("expected fake dota2 to exist")
	}

	// Create enforcer with custom home directory
	logger, _ := zap.NewDevelopment()
	pm := infra.NewProcessManager()
	fs := infra.NewFileSystemManagerWithHome(tmpDir)

	// Create a custom policy that points to our test directory
	customSteamPolicy := policy.NewSteamPolicyWithHome(tmpDir)
	customDota2Policy := policy.NewDota2PolicyWithHome(tmpDir)
	reg := policy.NewRegistryWithPolicies(customSteamPolicy, customDota2Policy)

	// Create a policy store adapter
	policyStore := &testPolicyStore{registry: reg}

	enforcer := usecase.NewEnforcer(pm, fs, policyStore, logger)

	// Run enforcement
	ctx := context.Background()
	results, err := enforcer.Enforce(ctx)
	if err != nil {
		t.Fatalf("enforcement failed: %v", err)
	}

	// Check results
	var totalDeleted int
	for _, r := range results {
		totalDeleted += len(r.DeletedPaths)
	}

	// Verify paths were deleted
	if fakeSteam.Exists() {
		t.Error("expected fake steam to be deleted")
	}
	if fakeSteam.Dota2Exists() {
		t.Error("expected fake dota2 to be deleted")
	}

	t.Logf("Deleted %d paths", totalDeleted)
}

func TestEnforcer_HandlesNonExistentPaths(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "appmon-integration-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Don't create any fake structure - paths don't exist

	logger, _ := zap.NewDevelopment()
	pm := infra.NewProcessManager()
	fs := infra.NewFileSystemManagerWithHome(tmpDir)

	customSteamPolicy := policy.NewSteamPolicyWithHome(tmpDir)
	reg := policy.NewRegistryWithPolicies(customSteamPolicy)
	policyStore := &testPolicyStore{registry: reg}

	enforcer := usecase.NewEnforcer(pm, fs, policyStore, logger)

	// Run enforcement - should not error even if paths don't exist
	ctx := context.Background()
	results, err := enforcer.Enforce(ctx)
	if err != nil {
		t.Fatalf("enforcement failed: %v", err)
	}

	// Check that no paths were deleted (because none existed)
	var totalDeleted int
	for _, r := range results {
		totalDeleted += len(r.DeletedPaths)
	}

	if totalDeleted != 0 {
		t.Errorf("expected 0 deleted paths, got %d", totalDeleted)
	}
}

func TestRegistry_PersistsAcrossRestarts(t *testing.T) {
	// Create temp directory for registry
	tmpDir, err := os.MkdirTemp("", "appmon-registry-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	registryPath := filepath.Join(tmpDir, ".test_registry")
	pm := infra.NewProcessManager()

	// Create first registry instance and register a daemon
	registry1 := infra.NewFileRegistryWithPath(registryPath, pm)

	daemon := domain.Daemon{
		PID:            12345,
		Role:           domain.RoleWatcher,
		ObfuscatedName: "com.apple.test.watcher",
	}

	if err := registry1.Register(daemon); err != nil {
		t.Fatalf("failed to register: %v", err)
	}

	// Create second registry instance (simulating restart)
	registry2 := infra.NewFileRegistryWithPath(registryPath, pm)

	// Verify data persisted
	entry, err := registry2.GetAll()
	if err != nil {
		t.Fatalf("failed to get all: %v", err)
	}

	if entry.WatcherPID != 12345 {
		t.Errorf("expected PID 12345, got %d", entry.WatcherPID)
	}

	if entry.WatcherName != "com.apple.test.watcher" {
		t.Errorf("expected name 'com.apple.test.watcher', got '%s'", entry.WatcherName)
	}
}

// testPolicyStore adapts policy.Registry to domain.PolicyStore
type testPolicyStore struct {
	registry *policy.Registry
}

func (s *testPolicyStore) GetAll() []domain.Policy {
	policies := s.registry.GetAll()
	result := make([]domain.Policy, len(policies))
	for i, p := range policies {
		result[i] = policy.ToPolicy(p)
	}
	return result
}

func (s *testPolicyStore) GetByID(id string) (*domain.Policy, error) {
	p, ok := s.registry.Get(id)
	if !ok {
		return nil, nil
	}
	pol := policy.ToPolicy(p)
	return &pol, nil
}

func (s *testPolicyStore) List() []string {
	return s.registry.List()
}
