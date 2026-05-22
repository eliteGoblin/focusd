package osadapter

import (
	"fmt"
	"os"
	"runtime"
)

// baseAdapter holds the OS-agnostic parts of every adapter. OS-specific
// files supply the name and the base-dir resolver; everything else
// (identity, run-mode detection, path layout, lifecycle stubs) is shared
// so the per-OS files stay tiny.
type baseAdapter struct {
	name string
	// baseDirFor resolves the runtime root for a given mode on this OS.
	baseDirFor func(mode RunMode) (string, error)
}

func (a *baseAdapter) Name() string        { return a.name }
func (a *baseAdapter) CurrentOS() string   { return runtime.GOOS }
func (a *baseAdapter) CurrentArch() string { return runtime.GOARCH }

// DetectRunMode infers privilege from the running process: euid 0 means
// root/system, anything else (including -1 on Windows) means user.
func (a *baseAdapter) DetectRunMode() RunMode {
	if os.Geteuid() == 0 {
		return ModeSystem
	}
	return ModeUser
}

func (a *baseAdapter) CanRunAsUser() bool   { return true }
func (a *baseAdapter) CanRunAsSystem() bool { return os.Geteuid() == 0 }

func (a *baseAdapter) base(mode RunMode) (string, error) {
	if !mode.Valid() {
		return "", fmt.Errorf("osadapter: invalid run mode %q", mode)
	}
	return a.baseDirFor(mode)
}

func (a *baseAdapter) DefaultBaseDir(mode RunMode) (string, error) {
	return a.base(mode)
}

func (a *baseAdapter) DefaultConfigPath(mode RunMode) (string, error) {
	b, err := a.base(mode)
	if err != nil {
		return "", err
	}
	return configPathIn(b), nil
}

func (a *baseAdapter) DefaultPluginDir(mode RunMode) (string, error) {
	b, err := a.base(mode)
	if err != nil {
		return "", err
	}
	return pluginDirIn(b), nil
}

func (a *baseAdapter) DefaultLogDir(mode RunMode) (string, error) {
	b, err := a.base(mode)
	if err != nil {
		return "", err
	}
	return logDirIn(b), nil
}

func (a *baseAdapter) DefaultStateDir(mode RunMode) (string, error) {
	b, err := a.base(mode)
	if err != nil {
		return "", err
	}
	return stateDirIn(b), nil
}

// Lifecycle: declared on the interface for forward compatibility, wired
// in a later phase. See ErrNotImplemented.
func (a *baseAdapter) InstallAgent(RunMode) error             { return ErrNotImplemented }
func (a *baseAdapter) UninstallAgent(RunMode) error           { return ErrNotImplemented }
func (a *baseAdapter) IsAgentInstalled(RunMode) (bool, error) { return false, ErrNotImplemented }
func (a *baseAdapter) StartAgent(RunMode) error               { return ErrNotImplemented }
func (a *baseAdapter) StopAgent(RunMode) error                { return ErrNotImplemented }
func (a *baseAdapter) IsAgentRunning(RunMode) (bool, error)   { return false, ErrNotImplemented }
