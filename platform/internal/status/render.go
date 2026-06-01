package status

import (
	"encoding/json"
	"fmt"
	"io"
)

// ANSI colours; suppressed when color=false (NO_COLOR / --no-color).
const (
	cReset  = "\033[0m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cGray   = "\033[90m"
)

func verdictColor(v Verdict) string {
	switch v {
	case Healthy:
		return cGreen
	case Degraded, Unavailable, Unknown:
		return cYellow
	default: // Disabled
		return cGray
	}
}

// RenderJSON writes the report as indented JSON. All fields are primitives,
// so machine output carries no disguised identifier.
func RenderJSON(r Report, out io.Writer) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fmt.Fprintln(out, `{"overall":"UNKNOWN"}`)
		return
	}
	out.Write(b)
	fmt.Fprintln(out)
}

// RenderText writes the human-readable report. Prints only job ids,
// statuses, and verdicts — never a path, label, or anchor.
func RenderText(r Report, out io.Writer, color bool) {
	paint := func(c, txt string) string {
		if !color {
			return txt
		}
		return c + txt + cReset
	}
	for _, j := range r.Jobs {
		fmt.Fprintf(out, "  %-26s %s\n", jobLabel(j.ID), paint(verdictColor(j.Verdict), jobText(j)))
	}
	fmt.Fprintf(out, "  %-26s %s\n", "OVERALL", paint(verdictColor(r.Overall), string(r.Overall)))
}

func jobText(j JobStatus) string {
	switch j.Verdict {
	case Disabled:
		return "disabled"
	case Unavailable:
		return "unavailable (needs admin install)"
	case Unknown:
		return "no runs yet"
	default:
		return fmt.Sprintf("%s · %s ago", j.Status, j.Age)
	}
}

// jobLabel maps a job_id to a short, stable display label. Unknown ids
// fall through to the raw job_id (never a disguised token).
func jobLabel(jobID string) string {
	switch jobID {
	case "dns-block-reconcile":
		return "dns-block (site blocklist)"
	case "kill-steam-reconcile":
		return "kill-steam (app removal)"
	case "skill-protector-reconcile":
		return "skill-guard (Claude skill)"
	case "network-block-reconcile":
		return "net-block (packet filter)"
	default:
		return jobID
	}
}
