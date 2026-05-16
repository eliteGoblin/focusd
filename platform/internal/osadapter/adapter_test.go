package osadapter

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewAdapterIdentity(t *testing.T) {
	a := NewAdapter()
	if a.CurrentOS() != runtime.GOOS {
		t.Errorf("CurrentOS = %q, want %q", a.CurrentOS(), runtime.GOOS)
	}
	if a.CurrentArch() != runtime.GOARCH {
		t.Errorf("CurrentArch = %q, want %q", a.CurrentArch(), runtime.GOARCH)
	}
	if a.Name() == "" {
		t.Error("Name is empty")
	}
}

func TestDetectRunModeMatchesCapability(t *testing.T) {
	a := NewAdapter()
	mode := a.DetectRunMode()
	if !mode.Valid() {
		t.Fatalf("DetectRunMode returned invalid mode %q", mode)
	}
	if mode == ModeSystem && !a.CanRunAsSystem() {
		t.Error("detected system mode but CanRunAsSystem is false")
	}
	if !a.CanRunAsUser() {
		t.Error("CanRunAsUser should always be true")
	}
}

func TestDefaultPathsLayoutAndIsolation(t *testing.T) {
	a := NewAdapter()

	for _, mode := range []RunMode{ModeUser, ModeSystem} {
		base, err := a.DefaultBaseDir(mode)
		if err != nil {
			t.Fatalf("DefaultBaseDir(%s): %v", mode, err)
		}
		if !strings.HasSuffix(base, AppName) {
			t.Errorf("%s base %q does not end with app name %q", mode, base, AppName)
		}

		cfg, _ := a.DefaultConfigPath(mode)
		if want := filepath.Join(base, "config.yaml"); cfg != want {
			t.Errorf("config path = %q, want %q", cfg, want)
		}
		pd, _ := a.DefaultPluginDir(mode)
		if want := filepath.Join(base, "plugins"); pd != want {
			t.Errorf("plugin dir = %q, want %q", pd, want)
		}
		sd, _ := a.DefaultStateDir(mode)
		if want := filepath.Join(base, "state"); sd != want {
			t.Errorf("state dir = %q, want %q", sd, want)
		}
		ld, _ := a.DefaultLogDir(mode)
		if want := filepath.Join(base, "logs"); ld != want {
			t.Errorf("log dir = %q, want %q", ld, want)
		}
	}

	// User and system roots MUST be different (complete isolation).
	userBase, _ := a.DefaultBaseDir(ModeUser)
	sysBase, _ := a.DefaultBaseDir(ModeSystem)
	if userBase == sysBase {
		t.Errorf("user and system base dirs must differ, both = %q", userBase)
	}
}

func TestInvalidRunModeRejected(t *testing.T) {
	a := NewAdapter()
	if _, err := a.DefaultBaseDir(RunMode("bogus")); err == nil {
		t.Error("expected error for invalid run mode")
	}
}

func TestRunModeValid(t *testing.T) {
	cases := map[RunMode]bool{
		ModeUser:           true,
		ModeSystem:         true,
		RunMode(""):        false,
		RunMode("root"):    false,
		RunMode("SYSTEM"):  false,
	}
	for m, want := range cases {
		if got := m.Valid(); got != want {
			t.Errorf("RunMode(%q).Valid() = %v, want %v", string(m), got, want)
		}
	}
}

func TestLifecycleNotImplemented(t *testing.T) {
	a := NewAdapter()
	if err := a.InstallAgent(ModeUser); err != ErrNotImplemented {
		t.Errorf("InstallAgent err = %v, want ErrNotImplemented", err)
	}
	if err := a.UninstallAgent(ModeUser); err != ErrNotImplemented {
		t.Errorf("UninstallAgent err = %v", err)
	}
	if _, err := a.IsAgentInstalled(ModeUser); err != ErrNotImplemented {
		t.Errorf("IsAgentInstalled err = %v, want ErrNotImplemented", err)
	}
	if err := a.StartAgent(ModeUser); err != ErrNotImplemented {
		t.Errorf("StartAgent err = %v, want ErrNotImplemented", err)
	}
	if err := a.StopAgent(ModeUser); err != ErrNotImplemented {
		t.Errorf("StopAgent err = %v", err)
	}
	if _, err := a.IsAgentRunning(ModeUser); err != ErrNotImplemented {
		t.Errorf("IsAgentRunning err = %v", err)
	}
}

func TestAllPathMethodsRejectInvalidMode(t *testing.T) {
	a := NewAdapter()
	bad := RunMode("nope")
	checks := map[string]func(RunMode) (string, error){
		"DefaultBaseDir":    a.DefaultBaseDir,
		"DefaultConfigPath": a.DefaultConfigPath,
		"DefaultPluginDir":  a.DefaultPluginDir,
		"DefaultLogDir":     a.DefaultLogDir,
		"DefaultStateDir":   a.DefaultStateDir,
	}
	for name, fn := range checks {
		if _, err := fn(bad); err == nil {
			t.Errorf("%s(invalid) returned nil error", name)
		}
	}
}

func TestCanRunAsUserAlwaysTrue(t *testing.T) {
	if !NewAdapter().CanRunAsUser() {
		t.Error("CanRunAsUser must be true")
	}
}

func TestCanRunAsSystemMatchesEuid(t *testing.T) {
	a := NewAdapter()
	// Whatever the answer, it must equal the system-mode detection.
	wantSystem := a.DetectRunMode() == ModeSystem
	if a.CanRunAsSystem() != wantSystem {
		t.Errorf("CanRunAsSystem=%v but DetectRunMode system=%v",
			a.CanRunAsSystem(), wantSystem)
	}
}
