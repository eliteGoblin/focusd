package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Seams. Real implementations (signed policy fetch, process/launchd
// inspection, partner spawn, binary swap) are deferred; the tested spine
// drives them through these interfaces without rework.
type (
	// PolicySource returns the desired state. It MUST always return a
	// usable Desired in normal operation (it falls back to the local
	// signed cache internally); an error means this tick is skipped and
	// already-applied durable blocks remain in force (fail closed).
	PolicySource interface{ Desired() (Desired, error) }

	// Observer reports what the world looks like this tick.
	Observer interface{ Observe() (Observed, error) }

	// LeaseStore is the staggered-upgrade token: only one worker holds
	// it, it auto-expires (a crashed canary cannot wedge upgrades).
	LeaseStore interface {
		Acquire(ttl time.Duration) (bool, error)
		Release() error
	}

	// Actuator performs side effects. ExitForUpgrade triggers process
	// exit in production (launcher then starts the right binary); in
	// tests it is recorded. It returns ErrStopLoop to end the loop.
	Actuator interface {
		EnforcePolicy() error
		RespawnPartner() error
		RepairLaunch() error
		ExitForUpgrade(target string) error
		Alert(note string)
	}
)

// ErrStopLoop is returned by Actuator.ExitForUpgrade to stop the loop
// (the process is about to be replaced by the launcher).
var ErrStopLoop = errors.New("reconcile: stop loop for upgrade")

// Engine runs the reconcile loop. Decide is pure; Engine owns the
// tiered, fail-soft execution: a Tier-1/2 failure is logged + reported
// and never blocks Tier-0 (enforce / partner / launchd).
type Engine struct {
	Policy   PolicySource
	Obs      Observer
	Lease    LeaseStore
	Act      Actuator
	Log      *slog.Logger
	Cmp      VersionCompare
	IsBad    func(string) bool
	LeaseTTL time.Duration
}

func (e *Engine) cmp() VersionCompare {
	if e.Cmp != nil {
		return e.Cmp
	}
	return DefaultVersionCompare
}

// wantsCanary is the precise predicate for "an upgrade to a brand-new
// desired version is pending and could proceed if this worker is the
// canary" — the only situation in which we should grab the lease.
func wantsCanary(d Desired, o Observed, cmp VersionCompare, isBad func(string) bool) bool {
	good := o.GoodVersion
	if good == "" {
		good = o.MyVersion
	}
	if o.MyVersion != good {
		return false // catch-up path: no lease needed (good already proven)
	}
	if d.Version == "" || d.Version == good {
		return false
	}
	if cmp(d.Version, good) < 0 {
		return false // downgrade: refused, never a canary
	}
	if isBad != nil && isBad(d.Version) {
		return false
	}
	return true
}

// RunOnce performs exactly one reconcile tick. Returns the Decision and
// ErrStopLoop when the worker must exit for an upgrade.
func (e *Engine) RunOnce(ctx context.Context) (Decision, error) {
	desired, err := e.Policy.Desired()
	if err != nil {
		// Tier-1: cannot get intent. Durable blocks remain (fail
		// closed); retry next tick.
		e.report(1, "policy", err)
		return Decision{Note: "policy unavailable; durable state retained"}, nil
	}

	obs, err := e.Obs.Observe()
	if err != nil {
		e.report(0, "observe", err)
		return Decision{Note: "observe failed; tick skipped"}, nil
	}

	leaseHeld := false
	if wantsCanary(desired, obs, e.cmp(), e.IsBad) {
		ttl := e.LeaseTTL
		if ttl <= 0 {
			ttl = time.Minute
		}
		if held, lerr := e.Lease.Acquire(ttl); lerr != nil {
			e.report(1, "lease", lerr) // safety net behaviour on lease error
		} else {
			leaseHeld = held
		}
	}

	dec := Decide(desired, obs, leaseHeld, e.IsBad, e.cmp())
	return dec, e.execute(dec)
}

// execute applies a decision. Tier-0 actions are all attempted even if
// one fails (errors reported, never abort the others). The upgrade exit
// is performed last, after enforcement, so the world is asserted before
// the process hands off.
func (e *Engine) execute(dec Decision) error {
	for _, a := range dec.Actions {
		switch a {
		case ActEnforcePolicy:
			e.tier0("enforce", e.Act.EnforcePolicy)
		case ActRespawnPartner:
			e.tier0("respawn_partner", e.Act.RespawnPartner)
		case ActRepairLaunch:
			e.tier0("repair_launch", e.Act.RepairLaunch)
		case ActRefuseDowngrade, ActSkipBadVersion:
			e.Act.Alert(dec.Note)
		}
	}
	if dec.Has(ActExitForUpgrade) {
		if err := e.Act.ExitForUpgrade(dec.TargetVersion); err != nil {
			return err // typically ErrStopLoop
		}
	}
	return nil
}

func (e *Engine) tier0(name string, fn func() error) {
	if err := fn(); err != nil {
		e.report(0, name, err) // logged, but the loop keeps going
	}
}

func (e *Engine) report(tier int, where string, err error) {
	if e.Log != nil {
		e.Log.Warn("reconcile step failed",
			"tier", tier, "where", where, "err", err)
	}
}

// Run loops RunOnce every interval until the context is cancelled or an
// upgrade exit is requested.
func (e *Engine) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := e.RunOnce(ctx); err != nil {
			if errors.Is(err, ErrStopLoop) {
				return nil // launcher will start the correct version
			}
			return fmt.Errorf("reconcile loop: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
