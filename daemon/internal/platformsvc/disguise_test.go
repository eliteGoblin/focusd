package platformsvc

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/relocate"
)

// TestPlatformStartCommandHasZeroLeaks is the HF4 (FEATURE 24 / P0) greppability
// guard for the PLATFORM child. It reconstructs exactly the command a real
// disguised install launches and asserts the RENDERED argv — the bytes `ps aux`
// shows — plus the environment `ps -E` shows contain NONE of the tokens a
// weak-moment `grep` would pivot on: the version, the literal 'platform',
// 'focusd', or the workdir path. Post-P0 the workdir rides on NEITHER argv NOR
// env — the disguised child self-derives it from its own binary location — and
// the inherited mesh role marker (MeshEnvKey) is scrubbed too.
func TestPlatformStartCommandHasZeroLeaks(t *testing.T) {
	const (
		salt    = "0123456789abcdef0123456789abcdef" // a realistic per-install salt
		version = "v0.16.7"
	)
	// A realistic disguised workdir (looks like an Apple metadata dir) + the
	// disguised, version-bearing binary path the store derives.
	workdir := "/Users/x/Library/Application Support/.com.apple.metadata.helper.9f3a2c/pw"
	binBase := relocate.PlatformBinBase(salt, version)
	binPath := filepath.Join(workdir, "bin", binBase)

	// Simulate the daemon's OWN inherited environment: launchd hands it the mesh
	// role marker, and (pre-P0) the workdir was appended for the child. BOTH must
	// be scrubbed from the disguised child so neither surfaces in `ps -E`.
	t.Setenv(MeshEnvKey, "run:a")
	t.Setenv(WorkdirEnvKey, workdir)

	p := &ProcSvc{Workdir: workdir, Argv0: relocate.PlatformArgv0(salt)}
	args, env := p.childArgvEnv(binPath)

	argv := strings.Join(args, " ") // what `ps -o args` / `ps aux` renders

	// The greppable-leak set — every token a searcher would try.
	leaks := map[string]string{
		"version tag":      "v0.16",
		"literal platform": "platform",
		"literal focusd":   "focusd",
		"workdir path":     workdir,
		"binary path":      binPath,
		"binary basename":  binBase,
		"--workdir flag":   "--workdir",
	}
	for name, tok := range leaks {
		if strings.Contains(argv, tok) {
			t.Errorf("platform start argv leaks %s (%q): argv = %q", name, tok, argv)
		}
	}

	// Positive: argv must be exactly the innocuous [<token>, "run"].
	if len(args) != 2 || args[1] != "run" {
		t.Fatalf("expected argv [<token> run], got %v", args)
	}
	if args[0] == "" || strings.ContainsAny(args[0], "/. ") {
		t.Errorf("argv[0] token %q is not a clean single token", args[0])
	}

	// The workdir must ride on NEITHER argv NOR env, and the mesh marker must be
	// scrubbed — a live `ps -E` over the disguised child must show nothing tied
	// to the install.
	for _, kv := range env {
		if strings.HasPrefix(kv, WorkdirEnvKey+"=") {
			t.Errorf("env still carries the workdir key %s (must be scrubbed): %q", WorkdirEnvKey, kv)
		}
		if strings.HasPrefix(kv, MeshEnvKey+"=") {
			t.Errorf("env still carries the mesh role marker %s (must be scrubbed): %q", MeshEnvKey, kv)
		}
		if strings.Contains(kv, workdir) {
			t.Errorf("env leaks the workdir path: %q", kv)
		}
	}
	t.Logf("disguised platform start argv = %q; workdir on neither argv nor env", argv)
}

// TestLegacyStartUnchangedWithoutSalt pins that a no-salt (dev/test/e2e) install
// keeps the exact pre-HF4 argv and inherits the environment — the disguise is
// opt-in via the salt and never perturbs the legacy path.
func TestLegacyStartUnchangedWithoutSalt(t *testing.T) {
	p := &ProcSvc{Workdir: "/tmp/wd"} // no Argv0 ⇒ legacy
	args, env := p.childArgvEnv("/tmp/wd/bin/v1/platform")
	want := []string{"/tmp/wd/bin/v1/platform", "--workdir", "/tmp/wd"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Errorf("legacy argv = %v, want %v", args, want)
	}
	if env != nil {
		t.Errorf("legacy env must be nil (inherit), got %v", env)
	}
}
