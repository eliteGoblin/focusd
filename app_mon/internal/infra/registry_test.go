package infra

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// mockProcessManager is a test double for ProcessManager
type mockProcessManager struct {
	runningPIDs map[int]bool
}

func newMockProcessManager() *mockProcessManager {
	return &mockProcessManager{
		runningPIDs: make(map[int]bool),
	}
}

func (m *mockProcessManager) FindByName(pattern string) ([]int, error) {
	return nil, nil
}

func (m *mockProcessManager) Kill(pid int) error {
	delete(m.runningPIDs, pid)
	return nil
}

func (m *mockProcessManager) IsRunning(pid int) bool {
	return m.runningPIDs[pid]
}

func (m *mockProcessManager) GetCurrentPID() int {
	return os.Getpid()
}

func (m *mockProcessManager) SetRunning(pid int, running bool) {
	m.runningPIDs[pid] = running
}

func TestFileRegistry_RegisterAndGetAll(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "registry-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	registryPath := filepath.Join(tmpDir, ".test_registry")
	pm := newMockProcessManager()
	registry := NewFileRegistryWithPath(registryPath, pm)

	// Register a watcher daemon
	watcher := domain.Daemon{
		PID:            12345,
		Role:           domain.RoleWatcher,
		ObfuscatedName: "com.apple.test.watcher",
		StartedAt:      time.Now(),
	}

	if err := registry.Register(watcher); err != nil {
		t.Fatalf("failed to register watcher: %v", err)
	}

	// Get all and verify
	entry, err := registry.GetAll()
	if err != nil {
		t.Fatalf("failed to get all: %v", err)
	}

	if entry.WatcherPID != 12345 {
		t.Errorf("expected watcher PID 12345, got %d", entry.WatcherPID)
	}

	if entry.WatcherName != "com.apple.test.watcher" {
		t.Errorf("expected watcher name 'com.apple.test.watcher', got '%s'", entry.WatcherName)
	}
}

func TestFileRegistry_GetPartner(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "registry-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	registryPath := filepath.Join(tmpDir, ".test_registry")
	pm := newMockProcessManager()
	registry := NewFileRegistryWithPath(registryPath, pm)

	// Register both daemons
	watcher := domain.Daemon{
		PID:            12345,
		Role:           domain.RoleWatcher,
		ObfuscatedName: "com.apple.test.watcher",
	}
	guardian := domain.Daemon{
		PID:            12346,
		Role:           domain.RoleGuardian,
		ObfuscatedName: "com.apple.test.guardian",
	}

	registry.Register(watcher)
	registry.Register(guardian)

	// Watcher's partner should be guardian
	partner, err := registry.GetPartner(domain.RoleWatcher)
	if err != nil {
		t.Fatalf("failed to get partner: %v", err)
	}

	if partner.Role != domain.RoleGuardian {
		t.Errorf("expected guardian role, got %s", partner.Role)
	}
	if partner.PID != 12346 {
		t.Errorf("expected PID 12346, got %d", partner.PID)
	}
}

func TestFileRegistry_IsPartnerAlive(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "registry-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	registryPath := filepath.Join(tmpDir, ".test_registry")
	pm := newMockProcessManager()
	registry := NewFileRegistryWithPath(registryPath, pm)

	// Register guardian
	guardian := domain.Daemon{
		PID:            12346,
		Role:           domain.RoleGuardian,
		ObfuscatedName: "com.apple.test.guardian",
	}
	registry.Register(guardian)

	// Mark guardian as running in mock
	pm.SetRunning(12346, true)

	// Check if watcher's partner (guardian) is alive
	alive, err := registry.IsPartnerAlive(domain.RoleWatcher)
	if err != nil {
		t.Fatalf("failed to check partner alive: %v", err)
	}

	if !alive {
		t.Error("expected guardian to be alive")
	}

	// Kill guardian in mock
	pm.SetRunning(12346, false)

	alive, err = registry.IsPartnerAlive(domain.RoleWatcher)
	if err != nil {
		t.Fatalf("failed to check partner alive: %v", err)
	}

	if alive {
		t.Error("expected guardian to be dead")
	}
}

func TestFileRegistry_Clear(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "registry-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	registryPath := filepath.Join(tmpDir, ".test_registry")
	pm := newMockProcessManager()
	registry := NewFileRegistryWithPath(registryPath, pm)

	// Register a daemon
	watcher := domain.Daemon{
		PID:            12345,
		Role:           domain.RoleWatcher,
		ObfuscatedName: "com.apple.test.watcher",
	}
	registry.Register(watcher)

	// Clear
	if err := registry.Clear(); err != nil {
		t.Fatalf("failed to clear: %v", err)
	}

	// Verify cleared
	entry, err := registry.GetAll()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry != nil {
		t.Error("expected nil entry after clear")
	}
}
