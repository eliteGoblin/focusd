// Package testutil provides shared test doubles. FakeAdapter is an
// in-memory osadapter.Adapter whose paths point under a temp dir, so
// bootstrap and lifecycle code can be tested without touching real OS
// locations or requiring root.
package testutil

import (
	"path/filepath"

	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
)

// FakeAdapter implements osadapter.Adapter against a temp base dir.
type FakeAdapter struct {
	OS, Arch, Label string
	Mode            osadapter.RunMode // value DetectRunMode returns
	IsSystem        bool              // value CanRunAsSystem returns
	UserBase        string
	SystemBase      string

	// Fault injection for testing bootstrap error paths.
	FailLogDir   bool
	FailStateDir bool

	InstallCalled bool
}

// NewFakeAdapter builds a FakeAdapter with user/system roots under root.
func NewFakeAdapter(root string) *FakeAdapter {
	return &FakeAdapter{
		OS: "darwin", Arch: "arm64", Label: "fake",
		Mode:       osadapter.ModeUser,
		IsSystem:   false,
		UserBase:   filepath.Join(root, "user"),
		SystemBase: filepath.Join(root, "system"),
	}
}

func (f *FakeAdapter) Name() string                     { return f.Label }
func (f *FakeAdapter) CurrentOS() string                { return f.OS }
func (f *FakeAdapter) CurrentArch() string              { return f.Arch }
func (f *FakeAdapter) DetectRunMode() osadapter.RunMode { return f.Mode }
func (f *FakeAdapter) CanRunAsUser() bool               { return true }
func (f *FakeAdapter) CanRunAsSystem() bool             { return f.IsSystem }

func (f *FakeAdapter) base(m osadapter.RunMode) (string, error) {
	switch m {
	case osadapter.ModeSystem:
		return f.SystemBase, nil
	case osadapter.ModeUser:
		return f.UserBase, nil
	default:
		return "", osadapter.ErrNotImplemented
	}
}

func (f *FakeAdapter) DefaultBaseDir(m osadapter.RunMode) (string, error) {
	return f.base(m)
}
func (f *FakeAdapter) DefaultConfigPath(m osadapter.RunMode) (string, error) {
	b, err := f.base(m)
	return filepath.Join(b, "config.yaml"), err
}
func (f *FakeAdapter) DefaultPluginDir(m osadapter.RunMode) (string, error) {
	b, err := f.base(m)
	return filepath.Join(b, "plugins"), err
}
func (f *FakeAdapter) DefaultLogDir(m osadapter.RunMode) (string, error) {
	if f.FailLogDir {
		return "", osadapter.ErrNotImplemented
	}
	b, err := f.base(m)
	return filepath.Join(b, "logs"), err
}
func (f *FakeAdapter) DefaultStateDir(m osadapter.RunMode) (string, error) {
	if f.FailStateDir {
		return "", osadapter.ErrNotImplemented
	}
	b, err := f.base(m)
	return filepath.Join(b, "state"), err
}

func (f *FakeAdapter) InstallAgent(osadapter.RunMode) error { f.InstallCalled = true; return nil }
func (f *FakeAdapter) UninstallAgent(osadapter.RunMode) error { return nil }
func (f *FakeAdapter) IsAgentInstalled(osadapter.RunMode) (bool, error) {
	return f.InstallCalled, nil
}
func (f *FakeAdapter) StartAgent(osadapter.RunMode) error             { return nil }
func (f *FakeAdapter) StopAgent(osadapter.RunMode) error              { return nil }
func (f *FakeAdapter) IsAgentRunning(osadapter.RunMode) (bool, error) { return false, nil }

// compile-time interface check
var _ osadapter.Adapter = (*FakeAdapter)(nil)
