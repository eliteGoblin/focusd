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
	cRed    = "\033[31m"
	cGray   = "\033[90m"
)

func verdictColor(v Verdict) string {
	switch v {
	case Healthy:
		return cGreen
	case Degraded, Unknown:
		return cYellow
	case Down:
		return cRed
	default:
		return cGray
	}
}

// PlatformDetail is the passed-through `platform status` result. The daemon
// treats it as opaque: TextOutput is what the platform's own text renderer
// produced; JSON is the platform's machine report (status.Report shape)
// captured as raw bytes for structural JSON composition. Available is false
// when `platform status` could not be run cleanly — the daemon then reports
// its own facts and marks the platform section unavailable, never failing.
//
// REDACTION NOTE: this struct carries platform-OWNED output, which the
// platform guarantees is free of disguised identifiers (ADR-0011/0012 — it
// emits only job ids, statuses, age buckets, counts). The daemon passes it
// through verbatim; the daemon's OWN fields (the Snapshot) never leak.
type PlatformDetail struct {
	Available  bool
	ExitCode   int             // platform's exit code when it ran (0 healthy/unknown, 1 degraded)
	TextOutput string          // raw text from `platform status` (passthrough)
	JSON       json.RawMessage // raw json from `platform status --json`
}

// Verdict derives the platform's health verdict in the DAEMON's vocabulary so
// it can be folded into the combined OVERALL (BUG 2). It prefers the JSON
// report's "overall" field when present (the platform's authoritative self-
// verdict), otherwise falls back to the exit code: 0 → Healthy, 1 → Degraded.
//
// ok=false means "do not fold this in": the platform is UNAVAILABLE (it never
// produced a verdict). Per the architect's rule, an unavailable platform is a
// NOTE only — it must never by itself force a non-zero/down daemon exit.
func (pd PlatformDetail) Verdict() (Verdict, bool) {
	if !pd.Available {
		return Unknown, false
	}
	// Prefer the platform's own JSON "overall" when we have it.
	if len(pd.JSON) > 0 {
		var r struct {
			Overall string `json:"overall"`
		}
		if json.Unmarshal(pd.JSON, &r) == nil {
			switch Verdict(r.Overall) {
			case Healthy:
				return Healthy, true
			case Degraded:
				return Degraded, true
			case Down:
				return Down, true
			case Unknown:
				return Unknown, true
			}
			// "DISABLED"/"UNAVAILABLE" or anything unmapped → fall through to
			// the exit code, which is the coarse health signal.
		}
	}
	// Exit-code fallback: 0 = healthy/unknown (treat as Healthy for folding),
	// 1 = degraded. Codes >= 2 never reach here (they read as unavailable).
	if pd.ExitCode == 1 {
		return Degraded, true
	}
	return Healthy, true
}

// RenderText writes the combined human-readable snapshot: the daemon's own
// section, the platform passthrough section, then one OVERALL verdict.
// Prints only primitives — no path, label, binary name, or anchor.
func RenderText(s Snapshot, res Result, pd PlatformDetail, out io.Writer, color bool) {
	paint := func(c, txt string) string {
		if !color {
			return txt
		}
		return c + txt + cReset
	}

	fmt.Fprintln(out, "focusd daemon status")
	fmt.Fprintf(out, "  %-22s %s\n", "mode", s.Mode)

	// Engine (mesh roles).
	fmt.Fprintf(out, "  %-22s %s\n", "protection engine", engineLine(s))

	// Platform process + version.
	fmt.Fprintf(out, "  %-22s %s\n", "platform process", procLine(s))
	fmt.Fprintf(out, "  %-22s %s\n", "platform version", versionLine(s))

	// Out-of-band watchdog rail liveness (FEATURE 12 / ADR-0016). Only shown
	// where the rail applies (darwin); bools only, no paths.
	if s.WatchdogChecked {
		fmt.Fprintf(out, "  %-22s %s\n", "out-of-band watchdog", watchdogLine(s))
	}

	// Platform passthrough section.
	fmt.Fprintln(out, "platform protections")
	if pd.Available && pd.TextOutput != "" {
		// Verbatim passthrough; platform owns its own formatting + redaction.
		io.WriteString(out, pd.TextOutput)
		if pd.TextOutput[len(pd.TextOutput)-1] != '\n' {
			fmt.Fprintln(out)
		}
	} else {
		fmt.Fprintf(out, "  %-22s %s\n", "(detail)", "unavailable (platform process not reporting)")
	}

	// Overall.
	fmt.Fprintf(out, "%-24s %s\n", "OVERALL",
		paint(verdictColor(res.Verdict), string(res.Verdict)+" — "+res.Note))
}

// engineLine describes the mesh roles, honestly degrading to "unknown" under
// a system install queried without sudo.
func engineLine(s Snapshot) string {
	if s.MeshUnknown {
		return "unknown (re-run with sudo)"
	}
	if !s.Found {
		return "not installed"
	}
	return fmt.Sprintf("%d/%d roles running", s.MeshLoaded, s.MeshTotal)
}

func procLine(s Snapshot) string {
	if s.VersionsUnknown {
		return "unknown (re-run with sudo)"
	}
	switch {
	case s.ProcCount == 0:
		return "down"
	case s.ProcCount == 1:
		return "running"
	default:
		return fmt.Sprintf("running (%d processes — anomaly)", s.ProcCount)
	}
}

// watchdogLine describes the out-of-band watchdog rail in bools-only terms,
// WITHOUT naming the underlying mechanism (the rail's implementation is a
// disguised identifier — naming it tells a weak-moment user exactly where to
// look). "present" (rail + copy both there), "MISSING" (no rail), or
// "degraded (recovering)" — the rail exists but its binary copy is gone, the
// silently-broken state ADR-0016 says must be visible. Self-heals on the next
// in-band reconcile.
func watchdogLine(s Snapshot) string {
	switch {
	case s.WatchdogCron && s.WatchdogCopyOK:
		return "present"
	case s.WatchdogCron && !s.WatchdogCopyOK:
		return "degraded (recovering)"
	default:
		return "MISSING"
	}
}

func versionLine(s Snapshot) string {
	if s.VersionsUnknown {
		return "unknown (re-run with sudo)"
	}
	desired := s.Desired
	if desired == "" {
		desired = "none"
	}
	good := s.Good
	if good == "" {
		good = "none"
	}
	return fmt.Sprintf("desired=%s good=%s", desired, good)
}

// daemonJSON is the daemon-owned half of the combined JSON. All primitives,
// no disguised identifier — safe to marshal directly.
type daemonJSON struct {
	Mode            string `json:"mode"`
	MeshLoaded      int    `json:"mesh_loaded"`
	MeshTotal       int    `json:"mesh_total"`
	MeshUnknown     bool   `json:"mesh_unknown"`
	ProcCount       int    `json:"proc_count"`
	Desired         string `json:"desired"`
	Good            string `json:"good"`
	VersionsUnknown bool   `json:"versions_unknown"`
	WarmingUp       bool   `json:"warming_up"`
	Found           bool   `json:"found"`
	WatchdogChecked bool   `json:"watchdog_checked"`
	WatchdogCron    bool   `json:"watchdog_rail"` // rail presence; mechanism name deliberately not exposed
	WatchdogCopyOK  bool   `json:"watchdog_copy_ok"`
	Verdict         string `json:"verdict"`
	Note            string `json:"note"`
}

// combinedJSON is the structural composition of the daemon snapshot and the
// platform passthrough. We NEVER concatenate two JSON objects — the platform
// report is embedded as a nested value (or null), with a sibling status flag.
type combinedJSON struct {
	Daemon         daemonJSON      `json:"daemon"`
	Platform       json.RawMessage `json:"platform"`        // platform's report, or null
	PlatformStatus string          `json:"platform_status"` // "ok" | "unavailable"
	Overall        string          `json:"overall"`
}

// RenderJSON writes the combined machine report. The platform half is
// embedded structurally (json.RawMessage), never string-concatenated.
func RenderJSON(s Snapshot, res Result, pd PlatformDetail, out io.Writer) {
	c := combinedJSON{
		Daemon: daemonJSON{
			Mode:            s.Mode,
			MeshLoaded:      s.MeshLoaded,
			MeshTotal:       s.MeshTotal,
			MeshUnknown:     s.MeshUnknown,
			ProcCount:       s.ProcCount,
			Desired:         s.Desired,
			Good:            s.Good,
			VersionsUnknown: s.VersionsUnknown,
			WarmingUp:       s.WarmingUp,
			Found:           s.Found,
			WatchdogChecked: s.WatchdogChecked,
			WatchdogCron:    s.WatchdogCron,
			WatchdogCopyOK:  s.WatchdogCopyOK,
			Verdict:         string(res.Verdict),
			Note:            res.Note,
		},
		Overall: string(res.Verdict),
	}
	if pd.Available && isValidJSON(pd.JSON) {
		c.Platform = pd.JSON
		c.PlatformStatus = "ok"
	} else {
		c.Platform = json.RawMessage("null")
		c.PlatformStatus = "unavailable"
	}

	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		fmt.Fprintln(out, `{"overall":"UNKNOWN","platform_status":"unavailable"}`)
		return
	}
	out.Write(b)
	fmt.Fprintln(out)
}

// isValidJSON guards the embed: a non-empty, well-formed platform report. If
// the platform somehow emitted garbage, we embed null rather than splice
// invalid bytes into the composed document.
func isValidJSON(b json.RawMessage) bool {
	return len(b) > 0 && json.Valid(b)
}
