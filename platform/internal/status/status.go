// Package status implements `platform status`: a read-only, plugin-aware
// health report the platform produces about ITSELF and its jobs.
//
// Layering (ADR-0012): the daemon is deliberately plugin-agnostic — it
// supervises the platform process and the launchd mesh, nothing more. All
// plugin/job detail lives here, in the platform, which already owns the
// job config and the run-history state DB. `daemon status` execs this and
// passes the result through. So the daemon never has to know a plugin
// exists, and never grows a dependency on the platform's state schema.
//
// The report carries ONLY non-disguised primitives (job ids, statuses,
// coarse age buckets, verdicts). It never emits a path, a launchd label,
// or a pf anchor — there is nothing here a weak-moment self could use to
// tear protection down.
package status

import "time"

// Verdict is a job's (and the report's) health classification.
type Verdict string

const (
	Healthy     Verdict = "HEALTHY"
	Degraded    Verdict = "DEGRADED"
	Tampered    Verdict = "TAMPERED"    // plugin binary was swapped + restored (ADR-0019)
	Disabled    Verdict = "DISABLED"    // job present but turned off in config
	Unavailable Verdict = "UNAVAILABLE" // job couldn't run here (reduced coverage)
	Unknown     Verdict = "UNKNOWN"     // no run recorded yet (fresh install)
)

// AgeBucket is a coarse recency classification. Precise timestamps add no
// operator value and only risk fingerprinting, so we bucket.
type AgeBucket string

const (
	AgeUnder1m AgeBucket = "<1m"
	AgeUnder5m AgeBucket = "<5m"
	AgeUnder1h AgeBucket = "<1h"
	AgeOver1h  AgeBucket = ">1h"
	AgeNever   AgeBucket = "never"
)

// staleAfter is how long an otherwise-OK job may go without running before
// it is flagged DEGRADED (stale). Reconcile jobs run every few minutes, so
// an hour with no run means something is wrong.
const staleAfter = AgeOver1h

// JobInput is the minimal job descriptor the report needs from config.
type JobInput struct {
	ID      string
	Enabled bool
}

// LastRunFn returns the most recent run for a job: its terminal status and
// start time. found=false means no run row exists yet (fresh / never ran).
// An error is treated by Collect as "unknown" for that job, not fatal.
type LastRunFn func(jobID string) (status string, startedAt time.Time, found bool, err error)

// TamperLookupFn returns, for a job, the time of its most-recent
// tamper-repaired event and how many fell in the lookback window;
// found=false means none. Read-only (mirrors LastRunFn). It is the seam
// that lets status flip a job to Tampered even over an "ok" run row, so a
// substitute binary that exits cleanly can never buy a green light
// (ADR-0019, AC-2). A nil TamperLookupFn (or one that errors) degrades
// gracefully: no tamper info, never a crash.
type TamperLookupFn func(jobID string) (since time.Time, count int, found bool)

// tamperWindow is how far back a tamper-repaired event still flips a job's
// verdict to Tampered even if a later clean run row exists. A repaired
// tamper stays visible for a day so a transient substitute-then-restore
// can't scroll off the status light immediately.
const tamperWindow = 24 * time.Hour

// JobStatus is one job's last-run summary. No disguised identifiers.
type JobStatus struct {
	ID      string    `json:"id"`
	Enabled bool      `json:"enabled"`
	Status  string    `json:"status"` // last terminal status, or "none"
	Age     AgeBucket `json:"age"`
	Verdict Verdict   `json:"verdict"`
	// TamperCount is how many tamper-repaired events fell in the lookback
	// window when Verdict==Tampered; 0 otherwise. Renders "repaired Nx".
	TamperCount int `json:"tamper_count,omitempty"`
}

// Report is the whole platform self-report.
type Report struct {
	Mode    string      `json:"mode"` // "user" | "system"
	Jobs    []JobStatus `json:"jobs"`
	Overall Verdict     `json:"overall"`
}

// Collect builds the report from the configured jobs, a run-history
// lookup, and a tamper lookup. Pure and deterministic given its inputs +
// now, so it is fully unit-testable without a real DB. tamperLookup may be
// nil (no integrity history available) — every job then reads as before.
func Collect(mode string, jobs []JobInput, lastRun LastRunFn, tamperLookup TamperLookupFn, now time.Time) Report {
	r := Report{Mode: mode, Jobs: make([]JobStatus, 0, len(jobs))}
	for _, j := range jobs {
		r.Jobs = append(r.Jobs, jobStatus(j, lastRun, tamperLookup, now))
	}
	r.Overall = overall(r.Jobs)
	return r
}

func jobStatus(j JobInput, lastRun LastRunFn, tamperLookup TamperLookupFn, now time.Time) JobStatus {
	js := JobStatus{ID: j.ID, Enabled: j.Enabled}
	if !j.Enabled {
		js.Status = "disabled"
		js.Age = AgeNever
		js.Verdict = Disabled
		return js
	}
	status, startedAt, found, err := lastRun(j.ID)
	if err != nil || !found {
		js.Status = "none"
		js.Age = AgeNever
		js.Verdict = Unknown // no run yet — aggregator treats as warming up
		// A tamper with no clean run row still must surface (a swapped
		// binary that never produced an ok row).
		return applyTamper(js, j.ID, tamperLookup, time.Time{}, false, now)
	}
	js.Status = status
	js.Age = bucketAge(now.Sub(startedAt))
	js.Verdict = jobVerdict(status, js.Age)
	return applyTamper(js, j.ID, tamperLookup, startedAt, true, now)
}

// applyTamper overrides a job's verdict to Tampered when a tamper-repaired
// event is newer than its last clean run (or there is no clean run) and
// within tamperWindow. This is the false-green kill (ADR-0019, AC-2): a
// substitute binary that exits 0 leaves an "ok" run row, but the recorded
// tamper makes the job read Tampered, not Healthy. The TamperCount is
// surfaced for "repaired Nx" rendering.
func applyTamper(js JobStatus, jobID string, lookup TamperLookupFn, lastRunAt time.Time, haveRun bool, now time.Time) JobStatus {
	if lookup == nil {
		return js
	}
	since, count, found := lookup(jobID)
	if !found || count == 0 {
		return js
	}
	if now.Sub(since) > tamperWindow {
		return js // too old to flip the light
	}
	// Flip when the tamper is at least as new as the last clean run — a
	// clean run that PREDATES the tamper must not mask it. If there is no
	// run row at all, any in-window tamper flips.
	if !haveRun || !since.Before(lastRunAt) {
		js.Verdict = Tampered
		js.TamperCount = count
	}
	return js
}

// jobVerdict maps a job's last terminal status + recency to health.
func jobVerdict(status string, age AgeBucket) Verdict {
	switch status {
	case "ok", "skipped":
		if age == staleAfter {
			return Degraded // ran fine but not recently → stale
		}
		return Healthy
	case "unavailable":
		// The job ran but reported it could not act here (e.g. a system
		// plugin under a user-mode install, or no console user). That is
		// REDUCED COVERAGE, not a config-disabled job — it must degrade
		// overall, never be silently ignored like Disabled.
		return Unavailable
	case "failed", "error", "timedout":
		return Degraded
	default:
		return Unknown
	}
}

// overall folds the per-job verdicts into one. Disabled jobs are ignored
// (a deliberately-off protection is not a failure). An Unavailable job is
// NOT ignored — it means reduced coverage and degrades the whole report
// (a user-mode install whose system jobs can't run must read DEGRADED, not
// HEALTHY/UNKNOWN). Worst wins: Unavailable ≈ Degraded > Unknown > Healthy.
// All-disabled/empty → Unknown.
func overall(jobs []JobStatus) Verdict {
	worst := Verdict("")
	// Tampered ranks at/above Degraded: a detected-and-repaired binary swap
	// is at least as serious as a failed run, and must dominate the report
	// (it can never be hidden behind a clean run). Worst wins.
	rank := map[Verdict]int{Healthy: 1, Unknown: 2, Unavailable: 3, Degraded: 3, Tampered: 4}
	for _, j := range jobs {
		if j.Verdict == Disabled {
			continue
		}
		if rank[j.Verdict] > rank[worst] {
			worst = j.Verdict
		}
	}
	if worst == "" {
		return Unknown
	}
	// Collapse Unavailable to Degraded at the report level: callers and exit
	// codes only need the coarse "something is reduced/broken" signal.
	if worst == Unavailable {
		return Degraded
	}
	return worst
}

// bucketAge classifies an elapsed duration into a coarse recency bucket.
func bucketAge(d time.Duration) AgeBucket {
	switch {
	case d < time.Minute: // includes clock-skew negatives → treat as fresh
		return AgeUnder1m
	case d < 5*time.Minute:
		return AgeUnder5m
	case d < time.Hour:
		return AgeUnder1h
	default:
		return AgeOver1h
	}
}
