package osadapter

import (
	"fmt"
	"strings"
	"time"
)

// args returns the daemon argv for a role. A/B run the supervised loop
// (`run --r <role> --mesh` — reconcile platform + recreate siblings);
// ensure runs the one-shot mesh repair (`ensure`).
//
// FEATURE 14 / ADR-0018: the PROD argv is minimized to "role + mesh
// marker" and nothing else — none of the disguised identifiers ride on
// the command line where `ps` exposes them to root. Specifically NOT
// baked: the three roster labels (the `launchctl bootout` keys, the worst
// leak), --github (a focusd-identity tell), --asset, --interval, and
// --workdir. A relaunched survivor reconstructs everything else:
//   - the roster from the masked workdir file (single source of truth),
//   - the workdir from filepath.Dir(os.Executable()) — the disguised binary
//     lives inside the workdir (argv[0] is unavoidably visible anyway),
//   - the github channel + platform asset are derived/compiled in.
//
// TEST-MODE EXCEPTION: e2e installs still bake --test-mode-flag + --workdir
// because the throwaway e2e workdir is NOT derivable from argv[0] (it is a
// caller-provided temp dir, and the binary is not relocated inside it). No
// prod identifiers are ever baked.
func args(s Spec, r Role) []string {
	var tail []string
	if s.isTest() {
		tail = []string{"--test-mode-flag", "true", "--workdir", s.Workdir}
	}
	if r == RoleEnsure {
		return append([]string{"ensure"}, tail...)
	}
	// --mesh: only an installed worker self-heals the launchd mesh.
	return append([]string{"run", "--r", string(r), "--mesh"}, tail...)
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
	fmt.Fprintf(&sb, "  <key>StandardErrorPath</key><string>%s/daemon.log</string>\n", s.Workdir)
	fmt.Fprintf(&sb, "  <key>StandardOutPath</key><string>%s/daemon.log</string>\n", s.Workdir)
	sb.WriteString("</dict></plist>\n")
	return sb.String()
}
