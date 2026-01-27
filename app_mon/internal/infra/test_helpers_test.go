package infra

import (
	"os"
	"time"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// mockProcessManager is a test double for ProcessManager
type mockProcessManager struct {
	runningPIDs map[int]bool
	killedPIDs  []int
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
	m.killedPIDs = append(m.killedPIDs, pid)
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

// mockDaemonRegistry is a test double for domain.DaemonRegistry
type mockDaemonRegistry struct {
	entry        *domain.RegistryEntry
	registryPath string
}

func newMockDaemonRegistry() *mockDaemonRegistry {
	return &mockDaemonRegistry{
		registryPath: "/tmp/mock-registry",
	}
}

func (m *mockDaemonRegistry) Register(daemon domain.Daemon) error {
	if m.entry == nil {
		m.entry = &domain.RegistryEntry{Version: 1}
	}
	switch daemon.Role {
	case domain.RoleWatcher:
		m.entry.WatcherPID = daemon.PID
		m.entry.WatcherName = daemon.ObfuscatedName
	case domain.RoleGuardian:
		m.entry.GuardianPID = daemon.PID
		m.entry.GuardianName = daemon.ObfuscatedName
	}
	m.entry.LastHeartbeat = time.Now().Unix()
	return nil
}

func (m *mockDaemonRegistry) GetPartner(role domain.DaemonRole) (*domain.Daemon, error) {
	return nil, nil
}

func (m *mockDaemonRegistry) UpdateHeartbeat(role domain.DaemonRole) error {
	return nil
}

func (m *mockDaemonRegistry) IsPartnerAlive(role domain.DaemonRole) (bool, error) {
	return false, nil
}

func (m *mockDaemonRegistry) GetAll() (*domain.RegistryEntry, error) {
	return m.entry, nil
}

func (m *mockDaemonRegistry) Clear() error {
	m.entry = nil
	return nil
}

func (m *mockDaemonRegistry) GetRegistryPath() string {
	return m.registryPath
}

func (m *mockDaemonRegistry) setEntry(wPID, gPID int) {
	m.entry = &domain.RegistryEntry{
		Version:       1,
		WatcherPID:    wPID,
		WatcherName:   "watcher",
		GuardianPID:   gPID,
		GuardianName:  "guardian",
		LastHeartbeat: time.Now().Unix(),
	}
}

// Ensure mockDaemonRegistry implements domain.DaemonRegistry
var _ domain.DaemonRegistry = (*mockDaemonRegistry)(nil)
