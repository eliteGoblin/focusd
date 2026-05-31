package status

import (
	"encoding/json"
	"fmt"
	"io"
)

// ANSI colours; suppressed when color=false (see --no-color / NO_COLOR).
const (
	cReset  = "\033[0m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cRed    = "\033[31m"
	cGray   = "\033[90m"
)

// verdictColor maps a verdict to its colour code.
func verdictColor(v Verdict) string {
	switch v {
	case Healthy:
		return cGreen
	case Degraded, Unavailable, Unknown:
		return cYellow
	case Down:
		return cRed
	default:
		return cGray
	}
}

// renderJSON writes the snapshot as indented JSON. Token fields marshal to
// "<redacted>" by construction, so machine output is safe too.
func renderJSON(s *Snapshot, out io.Writer) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		// Should never happen for our value types; emit a minimal, safe error.
		fmt.Fprintln(out, `{"overall":"UNKNOWN","note":"render error"}`)
		return
	}
	out.Write(b)
	fmt.Fprintln(out)
}

// renderText writes the human-readable snapshot. It prints ONLY primitives
// and verdicts — never a raw token (the Token type can't yield one anyway).
func renderText(s *Snapshot, out io.Writer, color bool) {
	paint := func(c, txt string) string {
		if !color {
			return txt
		}
		return c + txt + cReset
	}
	line := func(label, val string, v Verdict) {
		fmt.Fprintf(out, "  %-14s %s\n", label, paint(verdictColor(v), val))
	}

	fmt.Fprintf(out, "focusd status (%s install)\n", s.Mode)
	fmt.Fprintln(out, "")

	// Engine / mesh.
	line("engine", meshText(s.Mesh), s.Mesh.Verdict)

	// Platform.
	line("platform", platformText(s.Platform), s.Platform.Verdict)

	// Per-job protections.
	for _, j := range s.Jobs {
		line(jobLabel(j.JobID), jobText(j), j.Verdict)
	}

	// Hosts blocklist.
	line("blocklist", hostsText(s.Hosts), s.Hosts.Verdict)

	// pf table.
	line("packet-filter", pfText(s.Pf), s.Pf.Verdict)

	// Skill files.
	line("skill-files", fmt.Sprintf("%d/%d present", s.Skills.Present, s.Skills.Total), s.Skills.Verdict)

	fmt.Fprintln(out, "")
	overall := string(s.Overall)
	if s.Note != "" {
		overall += " — " + s.Note
	}
	fmt.Fprintf(out, "  %-14s %s\n", "OVERALL", paint(verdictColor(s.Overall), overall))
}

func meshText(m MeshHealth) string {
	if !m.Known {
		return "unknown (re-run with sudo)"
	}
	return fmt.Sprintf("%d/%d roles running", m.RolesRunning, m.RolesTotal)
}

func platformText(p PlatformHealth) string {
	desired := p.Desired
	if desired == "" {
		desired = "?"
	}
	good := p.Good
	if good == "" {
		good = "?"
	}
	return fmt.Sprintf("%d proc · desired %s · good %s", p.RunningProcs, desired, good)
}

func jobText(j JobHealth) string {
	if j.Verdict == Unavailable {
		return "unavailable (needs admin install)"
	}
	if j.Status == "none" {
		return "no runs yet"
	}
	return fmt.Sprintf("%s · %s ago", j.Status, j.Age)
}

func hostsText(h HostsHealth) string {
	if !h.MarkersPresent {
		return "block absent"
	}
	return fmt.Sprintf("%d domains blocked", h.BlockedCount)
}

func pfText(p PfHealth) string {
	if !p.Enabled {
		return "disabled (not configured)"
	}
	if !p.Known {
		return "unknown (re-run with sudo)"
	}
	return fmt.Sprintf("%d entries", p.Entries)
}

// jobLabel maps a job_id to a short, stable display label. Unknown ids
// fall through to the raw job_id (which is never a disguised token).
func jobLabel(jobID string) string {
	switch jobID {
	case "dns-block-reconcile":
		return "dns-block"
	case "kill-steam-reconcile":
		return "kill-steam"
	case "skill-protector-reconcile":
		return "skill-guard"
	case "network-block-reconcile":
		return "net-block"
	default:
		return jobID
	}
}
