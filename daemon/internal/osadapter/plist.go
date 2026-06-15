package osadapter

import (
	"fmt"
	"strings"
	"time"
)

// args returns the daemon argv for a role. A/B run the supervised loop
// (`run --r <role>` — reconcile platform + recreate siblings); ensure
// runs the one-shot mesh repair (`ensure`).
func args(s Spec, r Role) []string {
	common := []string{
		"--workdir", s.Workdir,
		"--github", s.Github,
		"--asset", s.Asset,
		"--interval", s.Interval.String(),
		"--test-mode-flag", boolStr(s.isTest()),
		// FEATURE 10 / ADR-0014: every role carries the FULL 3-label
		// roster (comma-joined, AllRoles order) so any survivor relaunched
		// cold reconstructs Spec.Roster from its own launch args and can
		// rebuild every sibling plist — no shared base, no separate
		// registry. The masked .roster workdir file is the cold-start /
		// sibling fallback when a plist isn't to hand.
		"--roster", rosterArg(s),
	}
	if r == RoleEnsure {
		return append([]string{"ensure"}, common...)
	}
	// --mesh: only an installed worker self-heals the launchd mesh.
	return append([]string{"run", "--r", string(r), "--mesh"}, common...)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// rosterArg renders the comma-joined 3-label roster baked into every
// plist's argv. It uses s.Label over AllRoles so the value reflects the
// ACTUAL resolved labels (test-mode e2e, disguised roster, or dev
// fallback) — whatever a survivor must rebuild. Aligned with AllRoles so
// `--r <role>` + this roster pins each sibling's label by position.
func rosterArg(s Spec) string {
	labels := make([]string, len(AllRoles))
	for i, r := range AllRoles {
		labels[i] = s.Label(r)
	}
	return strings.Join(labels, ",")
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
	}
	sb.WriteString("  <key>ProcessType</key><string>Background</string>\n")
	fmt.Fprintf(&sb, "  <key>StandardErrorPath</key><string>%s/daemon.log</string>\n", s.Workdir)
	fmt.Fprintf(&sb, "  <key>StandardOutPath</key><string>%s/daemon.log</string>\n", s.Workdir)
	sb.WriteString("</dict></plist>\n")
	return sb.String()
}
