package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/eliteGoblin/focusd/platform/internal/core/plugin"
)

// ScriptPlugin writes a POSIX-shell fake plugin binary and returns a
// plugin.Discovered pointing at it. Use for runner/scheduler tests that
// need real process execution without building Go binaries. Skips the
// test on Windows (no /bin/sh).
func ScriptPlugin(t *testing.T, id, scriptBody string) plugin.Discovered {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("script fake plugin requires POSIX shell")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, id)
	script := "#!/bin/sh\n" + scriptBody
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write script plugin: %v", err)
	}
	return plugin.Discovered{
		Manifest: &plugin.Manifest{
			ID: id, Name: id, Version: "1.0.0", Type: plugin.TypeJob,
			ProtocolVersion: "1", Entrypoint: "./" + id,
			SupportedOS: []string{runtime.GOOS}, SupportedArch: []string{runtime.GOARCH},
			RequiredPrivilege: plugin.PrivUser, RunAs: plugin.RunAsCurrentUser,
		},
		Dir: dir, BinaryPath: bin, OK: true,
	}
}
