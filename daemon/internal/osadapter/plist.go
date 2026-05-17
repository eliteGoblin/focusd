package osadapter

import (
	"fmt"
	"strings"
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
		// Every role carries the disguised base so any survivor can
		// recompute sibling labels and rebuild their plists — no
		// separate registry; the base lives in the plists (which must
		// exist anyway).
		"--mesh-base", s.base(),
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

// intervalSeconds is the StartInterval for the ensurer (min 1s).
func intervalSeconds(s Spec) int {
	n := int(s.Interval.Seconds())
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
