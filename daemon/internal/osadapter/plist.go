package osadapter

import (
	"fmt"
	"strings"
	"time"
)

// args returns the daemon argv for a role (the strings AFTER the binary in
// ProgramArguments).
//
// FEATURE 19 / ADR-0018: the PROD argv is now EMPTY — ProgramArguments is the
// binary alone. The role/mesh marker that FEATURE 14 had minimized to
// `run --r <role> --mesh` is moved entirely OFF the command line (where `ps`
// exposes it to root) and INTO the plist's EnvironmentVariables (see env /
// MeshEnvKey), which the process list does not display. So a `ps aux | grep
// mesh` (or a grep for the role flags) against the live mesh finds nothing.
// A relaunched member reconstructs this same legacy argv from the env via
// ArgvFromEnv; everything downstream (parse/loop/doEnsure/isMeshRole/
// deriveMeshWorkdir) is UNCHANGED — it gets the same argv it always did. The
// roster (masked workdir file), workdir (filepath.Dir(os.Executable())), and
// github/asset (derived/compiled in) are recovered exactly as before.
//
// TEST-MODE EXCEPTION: e2e installs still bake the FULL argv (`run --r <role>
// --mesh` / `ensure`) + --test-mode-flag + --workdir, because the throwaway
// e2e workdir is NOT derivable from argv[0] (it is a caller-provided temp dir,
// and the binary is not relocated inside it) and e2e must stay self-contained.
// Test mode emits NO env (see env), so the e2e flow is undisturbed.
func args(s Spec, r Role) []string {
	if !s.isTest() {
		return nil // PROD: binary-only argv; the marker rides in env (FEATURE 19)
	}
	tail := []string{"--test-mode-flag", "true", "--workdir", s.Workdir}
	if r == RoleEnsure {
		return append([]string{"ensure"}, tail...)
	}
	// --mesh: only an installed worker self-heals the launchd mesh.
	return append([]string{"run", "--r", string(r), "--mesh"}, tail...)
}

// DaemonLogName is the neutral basename of the daemon's launchd stdout/stderr
// log under the workdir (HF4 / FEATURE 24). Neutral ("run.log", not "daemon.log")
// so a filesystem grep for 'daemon' finds nothing tied to the supervisor.
const DaemonLogName = "run.log"

// envKV is one launchd EnvironmentVariables entry.
type envKV struct{ Key, Value string }

// env returns the launchd EnvironmentVariables entries for a role (FEATURE 19).
// PROD emits exactly ONE: MeshEnvKey=<encoded role> — the mesh marker the prod
// argv no longer carries. TEST mode emits NONE (nil): e2e keeps the full argv,
// so the environment stays clean and the e2e flow is undisturbed.
func env(s Spec, r Role) []envKV {
	if s.isTest() {
		return nil
	}
	return []envKV{{Key: MeshEnvKey, Value: encodeRole(r)}}
}

// EnsureBackstopInterval is the default ensurer StartInterval (FEATURE 10
// / ADR-0014). It is DECOUPLED from the worker reconcile cadence: launchd
// floors small StartInterval values, so pushing the ~2s in-process worker
// cadence here would be futile — the ensurer stays a ~10s backstop while
// the live A/B workers do the fast self-heal. Override via Spec.EnsureInterval.
const EnsureBackstopInterval = 10 * time.Second

// intervalSeconds is the StartInterval for the ensurer (min 1s). It uses
// Spec.EnsureInterval (the decoupled backstop cadence), NOT Spec.Interval
// (the worker reconcile cadence). Empty EnsureInterval → the backstop
// default. This keeps the ensurer at ~10s even when workers tick at ~2s.
func intervalSeconds(s Spec) int {
	d := s.EnsureInterval
	if d <= 0 {
		d = EnsureBackstopInterval
	}
	n := int(d.Seconds())
	if n < 1 {
		n = 1
	}
	return n
}

// Plist renders the launchd plist for a role. Pure → unit-tested.
// A/B: KeepAlive+RunAtLoad (survive kill/crash/reboot-at-login).
// ensure: RunAtLoad + StartInterval (periodic mesh repair).
func Plist(s Spec, r Role) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	sb.WriteString("<plist version=\"1.0\"><dict>\n")
	fmt.Fprintf(&sb, "  <key>Label</key><string>%s</string>\n", s.Label(r))
	sb.WriteString("  <key>ProgramArguments</key><array>\n")
	fmt.Fprintf(&sb, "    <string>%s</string>\n", s.SelfPath)
	for _, a := range args(s, r) {
		fmt.Fprintf(&sb, "    <string>%s</string>\n", a)
	}
	sb.WriteString("  </array>\n")
	// FEATURE 19: the role/mesh marker rides in EnvironmentVariables (PROD),
	// not argv — off the command line where `ps` would expose it. Emitted only
	// when non-empty (test mode keeps the full argv and emits no env).
	if kvs := env(s, r); len(kvs) > 0 {
		sb.WriteString("  <key>EnvironmentVariables</key><dict>\n")
		for _, kv := range kvs {
			fmt.Fprintf(&sb, "    <key>%s</key><string>%s</string>\n", kv.Key, kv.Value)
		}
		sb.WriteString("  </dict>\n")
	}
	sb.WriteString("  <key>RunAtLoad</key><true/>\n")
	if r == RoleEnsure {
		fmt.Fprintf(&sb, "  <key>StartInterval</key><integer>%d</integer>\n", intervalSeconds(s))
	} else {
		sb.WriteString("  <key>KeepAlive</key><true/>\n")
		// FEATURE 10 / ADR-0014: override launchd's 10s default respawn
		// throttle so a KILLED worker is relaunched in ~1s, not ~10s — the
		// process-kill half of the manual-bypass loophole. KeepAlive workers
		// are stable, so a low throttle does not cause respawn churn.
		sb.WriteString("  <key>ThrottleInterval</key><integer>1</integer>\n")
	}
	sb.WriteString("  <key>ProcessType</key><string>Background</string>\n")
	// HF4 (FEATURE 24): neutral log basename ("run.log", not "daemon.log") so a
	// filesystem grep for 'daemon' does not hit the supervisor's own log file.
	fmt.Fprintf(&sb, "  <key>StandardErrorPath</key><string>%s/%s</string>\n", s.Workdir, DaemonLogName)
	fmt.Fprintf(&sb, "  <key>StandardOutPath</key><string>%s/%s</string>\n", s.Workdir, DaemonLogName)
	sb.WriteString("</dict></plist>\n")
	return sb.String()
}
