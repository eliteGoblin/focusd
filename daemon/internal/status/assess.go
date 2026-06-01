// Package status implements `daemon status`: a read-only health snapshot
// of the focusd install that ends in one overall verdict.
//
// Layering (ADR-0012): the daemon is plugin-agnostic. This package reports
// ONLY daemon-owned facts — how many launchd mesh roles are running, whether
// the platform process is alive, and the platform version (desired vs
// last-known-good) — and DELEGATES protection detail to `platform status`,
// passing its output through. The daemon never reads the platform's state
// store and never learns a plugin exists.
//
// Redaction (ADR-0011): the assessor here is PURE and sees only primitives
// (counts, booleans, version strings). Disguised identifiers (the workdir,
// the launchd labels, the binary path, the pf anchor) never enter this file —
// they are confined to the IO shell (gather_*.go) behind redact.Token / Use.
// So nothing the assessor or renderer touches can leak a teardown string.
package status

// Verdict is the daemon-status health classification. Reused names mirror
// the platform's verdict vocabulary so the combined output reads coherently.
type Verdict string

const (
	Healthy  Verdict = "HEALTHY"
	Degraded Verdict = "DEGRADED"
	Down     Verdict = "DOWN"
	Unknown  Verdict = "UNKNOWN"
)

// Snapshot is the daemon's primitive-only view of its own state. By
// construction it holds NO disguised identifier — every field is a count,
// a boolean, or a (non-disguised) version string. The IO shell builds it;
// Assess consumes it; the renderer prints it.
type Snapshot struct {
	Mode string // "user" | "system"

	// Mesh: how many of the discovered launchd roles are loaded, out of how
	// many were found. MeshUnknown means we genuinely could not tell (e.g. a
	// system install queried without sudo — permission denied, NOT no-install).
	MeshLoaded  int
	MeshTotal   int
	MeshUnknown bool

	// ProcCount is how many live processes match the good platform binary
	// (exact path match). Expected 1 in steady state; 0 ⇒ down; >1 ⇒ anomaly.
	ProcCount int

	// Desired / Good are the platform version the daemon wants vs the last
	// version it promoted to good. VersionsUnknown means the workdir couldn't
	// be read (permission/absent) — distinct from "readable but no good yet".
	Desired         string
	Good            string
	VersionsUnknown bool

	// WarmingUp: a fresh install (<~10m old) with no good version yet. This
	// is HEALTHY, not DOWN — the first reconcile simply hasn't promoted yet.
	WarmingUp bool

	// PlatformUnavailable: `platform status` could not be run (down, timed
	// out, or non-zero exit). The daemon still reports its own facts.
	PlatformUnavailable bool

	// Found reports whether any genuine install was discovered at all.
	// found=false with no permission error ⇒ clean DOWN ("nothing installed").
	Found bool
}

// Result is the assessor's verdict plus a short, redaction-safe note.
type Result struct {
	Verdict Verdict
	Note    string
}

// Assess folds a Snapshot into one verdict. Pure and total — fully
// table-testable. The precedence is worst-wins for real failure, but
// "unknown" NEVER upgrades to degraded/down: an honest "can't tell" read
// (e.g. a system install without sudo) must not be reported as broken.
//
// Verdict rules (from feature 09 / architect review):
//
//	DOWN     — genuine mesh-down (found && loaded==0), OR a good version that
//	           should be running but its process is gone, OR nothing installed.
//	DEGRADED — partial mesh (0<loaded<total), OR version drift (desired!=good,
//	           good present).
//	HEALTHY  — warming up, or everything that should be up is up.
//	UNKNOWN  — we could not read enough to judge (folds to exit 0, never up).
func Assess(s Snapshot) Result {
	// No install discovered at all → clean DOWN, never internal-error. A
	// mode-name hint ("try with/without sudo") is fine; never a path hint.
	if !s.Found && !s.MeshUnknown {
		return Result{Down, "no install found (try with/without sudo for the other mode)"}
	}

	// Fresh install still warming up is HEALTHY, not broken.
	if s.WarmingUp {
		return Result{Healthy, "warming up (fresh install, no good version yet)"}
	}

	// Genuine mesh-down: install found, readable, but zero roles loaded.
	if s.Found && !s.MeshUnknown && s.MeshTotal > 0 && s.MeshLoaded == 0 {
		return Result{Down, "protection engine not running"}
	}

	// Good version exists and its process should be running but isn't.
	if s.Good != "" && s.ProcCount == 0 && !s.VersionsUnknown {
		return Result{Down, "platform process not running"}
	}

	// Partial mesh — some roles up, some not.
	if !s.MeshUnknown && s.MeshTotal > 0 && s.MeshLoaded > 0 && s.MeshLoaded < s.MeshTotal {
		return Result{Degraded, "some protection-engine roles are not running"}
	}

	// Version drift — daemon wants a different version than last-known-good.
	if !s.VersionsUnknown && s.Good != "" && s.Desired != "" && s.Desired != s.Good {
		return Result{Degraded, "platform version drift (desired differs from last-known-good)"}
	}

	// Process anomaly — more than one match for the good binary. Gated on
	// versions being KNOWN: an unknown read must never upgrade to Degraded
	// (ProcCount is meaningless when we couldn't read the workdir).
	if s.ProcCount > 1 && !s.VersionsUnknown {
		return Result{Degraded, "more than one platform process running (anomaly)"}
	}

	// Could not read mesh and/or versions, but nothing read as broken →
	// honest unknown (e.g. system install without sudo). Folds to exit 0.
	if s.MeshUnknown || s.VersionsUnknown {
		return Result{Unknown, "some facts unknown (re-run with sudo for full read)"}
	}

	return Result{Healthy, "all daemon-owned facts healthy"}
}

// ExitCode maps a verdict to the command's process exit code. Worst-wins
// health: Down(2) > Degraded(1) > Healthy(0). Unknown folds into 0 (mirrors
// `platform status`: Healthy||Unknown → 0) — an all-"unknown" read is not a
// failure, it is an honest "can't tell". Internal-error(3) is orthogonal and
// handled by the caller, NOT here.
func ExitCode(v Verdict) int {
	switch v {
	case Down:
		return 2
	case Degraded:
		return 1
	default: // Healthy, Unknown
		return 0
	}
}
