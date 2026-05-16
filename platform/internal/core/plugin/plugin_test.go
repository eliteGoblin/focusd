package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/eliteGoblin/focusd/platform/internal/core/state"
	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
)

func writePlugin(t *testing.T, root, name, manifest string, withBin bool) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if withBin {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func goodManifest(id string) string {
	return `{
  "id":"` + id + `","name":"X","version":"1.0.0","type":"job",
  "protocol_version":"1","entrypoint":"./` + id + `",
  "supported_os":["` + runtime.GOOS + `"],"supported_arch":["` + runtime.GOARCH + `"],
  "required_privilege":"user","run_as":"current_user"
}`
}

func TestParseManifestValid(t *testing.T) {
	m, err := ParseManifest([]byte(goodManifest("kill-steam")))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if !m.ProtocolSupported() || !m.SupportsHost(runtime.GOOS, runtime.GOARCH) {
		t.Error("valid manifest not recognized for host/protocol")
	}
}

func TestParseManifestAcceptsNativeBinaryRuntime(t *testing.T) {
	m := `{"id":"browser-monitor","name":"BM","version":"1.0.0","type":"job",
"runtime":"native_binary","protocol_version":"1","entrypoint":"./browser-monitor",
"supported_os":["` + runtime.GOOS + `"],"supported_arch":["` + runtime.GOARCH + `"],
"required_privilege":"user","run_as":"current_user"}`
	parsed, err := ParseManifest([]byte(m))
	if err != nil {
		t.Fatalf("native_binary runtime should be valid: %v", err)
	}
	if parsed.Runtime != RuntimeNativeBinary {
		t.Errorf("runtime = %q", parsed.Runtime)
	}
}

func TestParseManifestInvalid(t *testing.T) {
	cases := map[string]string{
		"bad json":      `{`,
		"unknown field": `{"id":"x","name":"X","version":"1","type":"job","protocol_version":"1","entrypoint":"./x","supported_os":["` + runtime.GOOS + `"],"supported_arch":["` + runtime.GOARCH + `"],"required_privilege":"user","run_as":"current_user","extra":1}`,
		"missing id":    `{"name":"X","version":"1","type":"job","protocol_version":"1","entrypoint":"./x","supported_os":["x"],"supported_arch":["y"],"required_privilege":"user","run_as":"current_user"}`,
		"bad type":      `{"id":"x","name":"X","version":"1","type":"weird","protocol_version":"1","entrypoint":"./x","supported_os":["x"],"supported_arch":["y"],"required_privilege":"user","run_as":"current_user"}`,
		"bad privilege": `{"id":"x","name":"X","version":"1","type":"job","protocol_version":"1","entrypoint":"./x","supported_os":["x"],"supported_arch":["y"],"required_privilege":"root","run_as":"current_user"}`,
		"bad run_as":    `{"id":"x","name":"X","version":"1","type":"job","protocol_version":"1","entrypoint":"./x","supported_os":["x"],"supported_arch":["y"],"required_privilege":"user","run_as":"god"}`,
		"empty os list": `{"id":"x","name":"X","version":"1","type":"job","protocol_version":"1","entrypoint":"./x","supported_os":[],"supported_arch":["y"],"required_privilege":"user","run_as":"current_user"}`,
		"bad runtime":   `{"id":"x","name":"X","version":"1","type":"job","runtime":"wasm","protocol_version":"1","entrypoint":"./x","supported_os":["` + runtime.GOOS + `"],"supported_arch":["` + runtime.GOARCH + `"],"required_privilege":"user","run_as":"current_user"}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(raw)); err == nil {
				t.Errorf("%s: expected error", name)
			}
		})
	}
}

func TestDiscoverAcceptsValidPlugin(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "kill-steam", goodManifest("kill-steam"), true)

	d := NewDiscoverer(osadapter.ModeUser)
	got, err := d.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 || !got[0].OK {
		t.Fatalf("expected 1 OK plugin, got %+v", got)
	}
	if got[0].BinaryPath == "" {
		t.Error("binary not resolved")
	}
}

func TestDiscoverMissingRootIsEmptyNoError(t *testing.T) {
	d := NewDiscoverer(osadapter.ModeUser)
	got, err := d.Discover(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil || got != nil {
		t.Errorf("missing root: got=%v err=%v, want nil,nil", got, err)
	}
}

func TestDiscoverRejectsUnsupportedOS(t *testing.T) {
	root := t.TempDir()
	m := `{"id":"x","name":"X","version":"1","type":"job","protocol_version":"1","entrypoint":"./x","supported_os":["plan9"],"supported_arch":["` + runtime.GOARCH + `"],"required_privilege":"user","run_as":"current_user"}`
	writePlugin(t, root, "x", m, true)
	got, _ := NewDiscoverer(osadapter.ModeUser).Discover(root)
	if got[0].OK || got[0].Reason == "" {
		t.Errorf("expected rejection for unsupported OS, got %+v", got[0])
	}
}

func TestDiscoverRejectsUnknownProtocol(t *testing.T) {
	root := t.TempDir()
	m := `{"id":"x","name":"X","version":"1","type":"job","protocol_version":"99","entrypoint":"./x","supported_os":["` + runtime.GOOS + `"],"supported_arch":["` + runtime.GOARCH + `"],"required_privilege":"user","run_as":"current_user"}`
	writePlugin(t, root, "x", m, true)
	got, _ := NewDiscoverer(osadapter.ModeUser).Discover(root)
	if got[0].OK {
		t.Error("expected rejection for unknown protocol")
	}
}

func TestDiscoverRejectsSystemPluginUnderUserMode(t *testing.T) {
	root := t.TempDir()
	m := `{"id":"sys","name":"S","version":"1","type":"job","protocol_version":"1","entrypoint":"./sys","supported_os":["` + runtime.GOOS + `"],"supported_arch":["` + runtime.GOARCH + `"],"required_privilege":"system","run_as":"system"}`
	writePlugin(t, root, "sys", m, true)
	got, _ := NewDiscoverer(osadapter.ModeUser).Discover(root)
	if got[0].OK {
		t.Error("user-mode platform must reject system plugin")
	}
}

func TestDiscoverSystemModeRejectsUserWritableDir(t *testing.T) {
	root := t.TempDir()
	dir := writePlugin(t, root, "x", goodManifest("x"), true)
	if err := os.Chmod(dir, 0o777); err != nil { // group/other writable
		t.Fatal(err)
	}
	got, _ := (&Discoverer{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Mode: osadapter.ModeSystem}).Discover(root)
	if got[0].OK {
		t.Error("system mode must reject user-writable plugin dir")
	}

	// Same plugin in user mode is fine (security rule is system-only).
	got2, _ := NewDiscoverer(osadapter.ModeUser).Discover(root)
	if !got2[0].OK {
		t.Errorf("user mode should accept; got %s", got2[0].Reason)
	}
}

func TestDiscoverRejectsMissingBinary(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "nob", goodManifest("nob"), false) // no binary
	got, _ := NewDiscoverer(osadapter.ModeUser).Discover(root)
	if got[0].OK {
		t.Error("expected rejection when binary missing")
	}
}

func TestDiscoverResolvesBinLayout(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "p")
	binDir := filepath.Join(dir, "bin", runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// entrypoint points nowhere; bin/<os>-<arch>/<id> is the fallback.
	m := `{"id":"p","name":"P","version":"1","type":"job","protocol_version":"1","entrypoint":"./missing","supported_os":["` + runtime.GOOS + `"],"supported_arch":["` + runtime.GOARCH + `"],"required_privilege":"user","run_as":"current_user"}`
	os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(m), 0o644)
	exe := filepath.Join(binDir, "p")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	os.WriteFile(exe, []byte("x"), 0o755)

	got, _ := NewDiscoverer(osadapter.ModeUser).Discover(root)
	if !got[0].OK || got[0].BinaryPath != exe {
		t.Errorf("bin-layout resolution failed: %+v", got[0])
	}
}

func TestSyncInventoryPersistsBothOutcomes(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	disc := []Discovered{
		{Manifest: &Manifest{ID: "ok", Name: "OK", Version: "1", Type: "job",
			ProtocolVersion: "1", Entrypoint: "./ok", SupportedOS: []string{"darwin"},
			SupportedArch: []string{"arm64"}, RequiredPrivilege: "user",
			RunAs: "current_user"}, Dir: "/p/ok", OK: true},
		{Manifest: &Manifest{ID: "bad"}, Dir: "/p/bad", OK: false, Reason: "boom"},
		{Manifest: nil, Dir: "/p/junk", OK: false, Reason: "unparseable"},
	}
	if err := SyncInventory(db, disc); err != nil {
		t.Fatalf("SyncInventory: %v", err)
	}
	all, _ := db.Plugins.List()
	if len(all) != 3 {
		t.Fatalf("inventory rows = %d, want 3", len(all))
	}
	okRow, _ := db.Plugins.Get("ok")
	if okRow.ValidationStatus != state.ValidationOK || okRow.SupportedOS != "darwin" {
		t.Errorf("ok row wrong: %+v", okRow)
	}
	badRow, _ := db.Plugins.Get("bad")
	if badRow.ValidationStatus != state.ValidationRejected || badRow.ValidationError != "boom" {
		t.Errorf("bad row wrong: %+v", badRow)
	}
}
