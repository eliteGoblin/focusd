// Package reconcile is the platform's self-protection spine: one
// idempotent control loop that drives the running system toward the
// signed desired state. Anti-kill, policy enforcement, and staggered
// self-upgrade are all just "make observed match desired" — not separate
// subsystems.
//
// Decide is a pure function (no I/O) so every scenario is exhaustively
// unit-tested; all side effects live behind the Actuator seam in
// engine.go. See app_mon/documents/design/self_protecting_reconcile_platform.md.
package reconcile

// Desired is the intent, read from the signed (future: remote) policy.
// It is tighten-only by design: there is no field here that disables
// enforcement — the "no inside door handle" rule.
type Desired struct {
	Version    string // desired_version (which binary should run)
	PolicyHash string // identity of the intended restriction set
}

// Observed is what a worker sees this tick about itself and the world.
type Observed struct {
	MyVersion       string // baked into this binary at build time
	GoodVersion     string // last proven-good version ("" => first boot)
	PolicyApplied   bool   // world currently matches Desired.PolicyHash
	PartnerAlive    bool   // sibling worker heartbeating
	LaunchEntriesOK bool   // launchd autostart entries present & loaded
}

// Action is a single thing the loop must do this tick.
type Action string

const (
	// Tier-0 maintenance — always evaluated, never suppressed by
	// version logic; the core must never be blocked by anything else.
	ActEnforcePolicy  Action = "enforce_policy"  // re-assert blocks (idempotent)
	ActRespawnPartner Action = "respawn_partner" // partner dead
	ActRepairLaunch   Action = "repair_launch"   // launchd entries missing

	// Version reconciliation.
	ActExitForUpgrade  Action = "exit_for_upgrade" // exit; launcher brings correct version
	ActRefuseDowngrade Action = "refuse_downgrade" // desired < good (rollback attack)
	ActSkipBadVersion  Action = "skip_bad_version" // desired marked bad
)

// Decision is the pure output of one reconcile evaluation.
type Decision struct {
	Actions       []Action
	TargetVersion string // set when ActExitForUpgrade is present
	Steady        bool   // nothing to do
	Note          string
}

func (d Decision) Has(a Action) bool {
	for _, x := range d.Actions {
		if x == a {
			return true
		}
	}
	return false
}

// VersionCompare returns -1, 0, 1 (strings.Compare semantics) for two
// version strings. Injected so Decide stays pure and testable.
type VersionCompare func(a, b string) int

// Decide is the heart of the system: a pure function mapping
// (desired, observed, lease, badness) to the actions to take.
//
//   - Tier-0 maintenance (enforce / respawn / repair) is ALWAYS included
//     when needed, regardless of any version transition.
//   - A worker only ever exits *itself* for version changes; it never
//     kills the partner for a version reason.
//   - Loosening is impossible here: no input yields "stop enforcing".
func Decide(d Desired, o Observed, leaseHeld bool, isBad func(string) bool, cmp VersionCompare) Decision {
	dec := Decision{}

	// --- Tier-0 maintenance: evaluated unconditionally. ---
	if !o.PolicyApplied {
		dec.Actions = append(dec.Actions, ActEnforcePolicy)
	}
	if !o.PartnerAlive {
		dec.Actions = append(dec.Actions, ActRespawnPartner)
	}
	if !o.LaunchEntriesOK {
		dec.Actions = append(dec.Actions, ActRepairLaunch)
	}

	// First boot: an empty GoodVersion means "trust what I am running".
	good := o.GoodVersion
	if good == "" {
		good = o.MyVersion
	}

	switch {
	case o.MyVersion != good:
		// I'm not on the proven-good version → catch up. good only ever
		// advances, so this is never a downgrade.
		dec.Actions = append(dec.Actions, ActExitForUpgrade)
		dec.TargetVersion = good
		dec.Note = "catch-up to good version"

	case d.Version != "" && d.Version != good:
		// A new desired version that is not yet proven good.
		switch {
		case cmp(d.Version, good) < 0:
			dec.Actions = append(dec.Actions, ActRefuseDowngrade)
			dec.Note = "refuse downgrade (desired < good)"
		case isBad != nil && isBad(d.Version):
			dec.Actions = append(dec.Actions, ActSkipBadVersion)
			dec.Note = "desired version marked bad; staying on good"
		case leaseHeld:
			dec.Actions = append(dec.Actions, ActExitForUpgrade)
			dec.TargetVersion = d.Version
			dec.Note = "canary: upgrading to desired"
		default:
			// Safety net: no lease → do not touch version, keep
			// enforcing. The canary will promote good when healthy.
			dec.Note = "safety-net: waiting for canary to promote"
		}

	default:
		// my == good == desired (or no desired pinned): aligned.
	}

	if len(dec.Actions) == 0 {
		dec.Steady = true
		if dec.Note == "" {
			dec.Note = "steady"
		}
	}
	return dec
}
