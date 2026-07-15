//go:build darwin

package osadapter

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
	"github.com/eliteGoblin/focusd/daemon/internal/platdir"
	"github.com/eliteGoblin/focusd/daemon/internal/relocate"
)

// TestSandboxDisguiseProve is the FEATURE-26 sandbox "prove": it builds a
// realistic install (a disguised daemon-home + platform-workdir, masked
// version/roster/pointer state) beside real-app lookalikes, then demonstrates —
// via the same grep/find a weak-moment user would run — that:
//
//   - no plaintext version string appears anywhere (grep -r <version> → nothing);
//   - no single glob spans the daemon-home AND the platform-workdir (they read as
//     DIFFERENT ordinary apps — no shared dot/prefix/tail);
//   - the legacy fixed literals (.roster / platform.lock / *platform*) are gone;
//   - the sweeps reap the planted stale focusd dir but NEVER a real-app fixture.
//
// It logs the sample disguised names under `go test -v` for human review.
func TestSandboxDisguiseProve(t *testing.T) {
	grep, gerr := exec.LookPath("grep")
	find, ferr := exec.LookPath("find")
	if gerr != nil || ferr != nil {
		t.Skip("grep/find not available")
	}

	root := t.TempDir()
	const version = "v0.16.7"

	// --- build a realistic disguised daemon-home ---
	daemonHome, err := relocate.FreshHiddenDir(root)
	if err != nil {
		t.Fatal(err)
	}
	// a fake daemon binary relocated inside (disguised basename)
	fakeBin := filepath.Join(t.TempDir(), "daemon")
	if err := os.WriteFile(fakeBin, []byte("\xcf\xfa\xed\xfeFAKE-MACHO"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := relocate.RelocateInto(fakeBin, daemonHome); err != nil {
		t.Fatal(err)
	}
	// seed the salt so all masks + salt-derived basenames engage
	salt := "cafef00ddeadbeefcafef00ddeadbeef"
	if err := os.WriteFile(filepath.Join(daemonHome, relocate.SaltBasename), []byte(salt), 0o600); err != nil {
		t.Fatal(err)
	}
	platdir.MarkDaemonHome(daemonHome)
	st := &core.Store{Dir: daemonHome}
	if err := st.WriteDesired(version); err != nil { // masked version.json
		t.Fatal(err)
	}
	if err := st.WriteGood(version); err != nil { // masked good
		t.Fatal(err)
	}
	if err := st.MarkBad("v0.16.8"); err != nil { // masked bad + keyed-digest name
		t.Fatal(err)
	}
	// masked roster at the salt-derived basename
	if err := core.WriteRoster(st.RosterPath(), []string{"com.apple.coreservices.spotlight", "MicrosoftUpdateHelper", "trustlocationd"}); err != nil {
		t.Fatal(err)
	}
	// pidfile at its salt-derived basename
	if err := os.WriteFile(st.PidFilePath(), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}

	// --- build the disposable platform-workdir + pointer ---
	platformWorkdir, err := platdir.Create(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := platdir.Write(daemonHome, platformWorkdir); err != nil { // masked pointer
		t.Fatal(err)
	}
	// engine files under the disguised bin basename (no 'platform'/version)
	binBase := relocate.PlatformBinBase(salt, version)
	if err := os.MkdirAll(filepath.Join(platformWorkdir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(platformWorkdir, "bin", binBase), []byte("ENGINE"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(platformWorkdir, "state.db"), []byte("sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}

	// --- real-app lookalikes + a stale focusd platform-workdir ---
	real := realAppLookalikes(t, root)
	stale, err := platdir.Create(root) // a stale (non-keep) platform-workdir
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("daemon-home       basename: %q", filepath.Base(daemonHome))
	t.Logf("platform-workdir  basename: %q", filepath.Base(platformWorkdir))
	t.Logf("stale-workdir     basename: %q", filepath.Base(stale))
	t.Logf("platform bin      basename: %q", binBase)

	// ---- PROVE 1: no plaintext version anywhere ----
	out, _ := exec.Command(grep, "-rl", version, root).CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("PROVE1 FAIL: plaintext version %q found on disk:\n%s", version, out)
	}
	t.Logf("PROVE1 ok: grep -r %s → no hit (version masked)", version)

	// ---- PROVE 2: no legacy fixed literal, and no single FOLDER-name glob a user
	// would try to hunt hidden/focusd dirs catches EITHER focusd folder — nor spans
	// both. The threat is the directory signature under App Support (what `ls` /
	// `find -maxdepth 1` shows); the disguised binary FILES inside are out of this
	// feature's naming scope (honest floor). ----
	dhBase := filepath.Base(daemonHome)
	pwBase := filepath.Base(platformWorkdir)

	// 2a. the de-patterned marker literals a NEW salted install must NOT write
	// (roster/pidfile/pointer/lock are now salt-derived). Scoped to the focusd
	// dirs, since a real-app lookalike fixture may plant a legacy-looking name.
	for _, dir := range []string{daemonHome, platformWorkdir} {
		for _, lit := range []string{".roster", "platform.lock", ".seq"} {
			out, _ := exec.Command(find, dir, "-name", lit).CombinedOutput()
			if strings.TrimSpace(string(out)) != "" {
				t.Fatalf("PROVE2 FAIL: de-patterned literal %q present in a focusd dir:\n%s", lit, out)
			}
		}
	}

	// 2b. no "hunting" folder-name glob catches either focusd folder, and none
	// spans both.
	for _, pat := range []string{".*", "*focusd*", "*platform*", "*daemon*", "*.??????????", "com.*.*.??????????"} {
		out, _ := exec.Command(find, root, "-maxdepth", "1", "-type", "d", "-name", pat).CombinedOutput()
		hitDaemon, hitPlatform := false, false
		for _, l := range nonEmptyLines(string(out)) {
			if filepath.Base(l) == dhBase {
				hitDaemon = true
			}
			if filepath.Base(l) == pwBase {
				hitPlatform = true
			}
		}
		if hitDaemon && hitPlatform {
			t.Fatalf("PROVE2 FAIL: folder glob %q spans BOTH focusd dirs (%q,%q)", pat, dhBase, pwBase)
		}
		// The blatant focusd/hidden hunts must miss BOTH.
		if pat == ".*" || pat == "*focusd*" || pat == "*platform*" || pat == "*daemon*" {
			if hitDaemon || hitPlatform {
				t.Fatalf("PROVE2 FAIL: hunting glob %q caught a focusd folder (%q,%q)", pat, dhBase, pwBase)
			}
		}
	}
	t.Logf("PROVE2 ok: no legacy literal; no folder glob spans daemon-home %q + platform-workdir %q", dhBase, pwBase)

	// ---- PROVE 3: sweeps reap the stale focusd dir, never a real app ----
	removed, err := SweepStalePlatformWorkdirs(root, platformWorkdir)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("PROVE3 FAIL: stale sweep removed %d, want 1", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("PROVE3 FAIL: stale platform-workdir not reaped")
	}
	if _, err := os.Stat(platformWorkdir); err != nil {
		t.Fatalf("PROVE3 FAIL: live platform-workdir reaped")
	}
	assertAllSurvive(t, "sandbox-prove", real)
	t.Logf("PROVE3 ok: stale workdir reaped, %d real-app fixtures survived", len(real))
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
