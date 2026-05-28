// Package core holds the daemon's pure decision logic: given what is
// observed (desired/running/good versions, bad set, config presence),
// decide the single next action. No I/O — exhaustively unit-tested.
// All side effects (download, start/stop, launchd, crash detection)
// live in the executor behind seams.
package core

// State is everything the decision needs, gathered each tick.
type State struct {
	HaveConfig bool            // version config file exists on disk
	Desired    string          // desired platform version (from config)
	Running    string          // running platform version; "" = none running
	Good       string          // last-known-good version; "" = none yet
	Bad        map[string]bool // versions that crash-looped → never run
}

// Kind is the action the executor must perform.
type Kind string

const (
	// EnsureRunning: make Target the running platform (download if the
	// binary is missing, then start). Covers "nothing running" and
	// "running wrong version" (Recreate: stop old, start Target).
	EnsureRunning Kind = "ensure_running"
	// Rollback: Desired is bad → run Good instead.
	Rollback Kind = "rollback"
	// Steady: running == desired, nothing to do.
	Steady Kind = "steady"
	// Blocked: the reconciler cannot make progress. Either: the desired
	// version is bad and there is no Good to fall back to; OR no
	// desired version is configured at all (the daemon does NOT
	// auto-update — `daemon install -v vX.Y.Z` and `daemon update`
	// set the desired version explicitly; the reconcile loop never
	// touches the network).
	Blocked Kind = "blocked"
)

// Action is the pure output of Decide.
type Action struct {
	Kind   Kind
	Target string // version EnsureRunning/Rollback should bring up
	Note   string
}

// Decide maps observed State to the one next Action. Pure, total,
// deterministic. The daemon's whole responsibility — "ensure the
// correct platform version is running, roll back a bad one" — is here.
func Decide(s State) Action {
	// 1. No desired version configured → Blocked. The daemon does not
	// auto-resolve "Latest" from any network source; a desired version
	// is set explicitly by `daemon install -v vX.Y.Z` (at install time)
	// or `daemon update [vX.Y.Z]` (later). The reconcile loop has no
	// network dependency in this path.
	if !s.HaveConfig || s.Desired == "" {
		return Action{Kind: Blocked,
			Note: "no desired version configured; run `daemon update vX.Y.Z` to set one"}
	}

	target := s.Desired

	// 2. Desired version is known-bad → fall back to Good (rollback).
	if s.Bad[target] {
		if s.Good == "" || s.Bad[s.Good] {
			return Action{Kind: Blocked, Note: "desired is bad and no good fallback"}
		}
		if s.Running == s.Good {
			return Action{Kind: Steady, Target: s.Good, Note: "running good; desired is bad"}
		}
		return Action{Kind: Rollback, Target: s.Good,
			Note: "desired " + target + " is bad → rollback to good " + s.Good}
	}

	// 3. Nothing running → bring up the desired version.
	if s.Running == "" {
		return Action{Kind: EnsureRunning, Target: target,
			Note: "no platform running → start " + target}
	}

	// 4. Running the desired version → steady.
	if s.Running == target {
		return Action{Kind: Steady, Target: target, Note: "running desired"}
	}

	// 5. Running a different version → Recreate to the desired one.
	return Action{Kind: EnsureRunning, Target: target,
		Note: "running " + s.Running + " ≠ desired " + target + " → switch"}
}
