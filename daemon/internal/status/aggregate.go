package status

import "time"

// freshInstallWindow is how young an install can be — with NO recorded job
// runs — to count as "warming up" (HEALTHY) rather than DEGRADED.
const freshInstallWindow = 10 * time.Minute

// Aggregate folds the individual probe results into one overall verdict +
// note, following the FEATURE 09 rules:
//
//   - DOWN   if the engine has 0 running roles, OR the hosts markers are
//     absent, OR all skill files are missing. These are the
//     "protection is actually gone" conditions.
//   - HEALTHY (warming up) for a fresh install (<10m, no job runs yet) —
//     not DEGRADED, so a brand-new install reads green.
//   - DEGRADED for any partial/stale/drift/unavailable-system condition
//     that isn't a hard DOWN.
//   - HEALTHY only when every applicable layer is healthy.
//
// installAgeKnown + installAge let the caller report warming-up; when the
// age is unknown (can't stat the workdir) we do NOT claim warming-up.
func Aggregate(s *Snapshot, installAge time.Duration, installAgeKnown bool) {
	// --- DOWN conditions (any one trips it) -------------------------------
	if s.Mesh.Known && s.Mesh.RolesRunning == 0 {
		s.Overall = Down
		s.Note = "protection engine is not running"
		return
	}
	if s.Hosts.Verdict == Down {
		s.Overall = Down
		s.Note = "site blocklist is missing from /etc/hosts"
		return
	}
	if s.Skills.Present == 0 {
		s.Overall = Down
		s.Note = "all Claude skill files are missing"
		return
	}

	// --- Fresh install: warming up = HEALTHY ------------------------------
	if installAgeKnown && installAge < freshInstallWindow && noJobRunsYet(s.Jobs) {
		s.WarmingUp = true
		s.Overall = Healthy
		s.Note = "warming up — first protection runs not recorded yet"
		return
	}

	// --- DEGRADED conditions ---------------------------------------------
	if s.Mode == "user" {
		// User install is the deliberate degraded fallback (ADR-0010): the
		// admin-level protections are Unavailable, so overall is DEGRADED.
		s.Overall = Degraded
		s.Note = "user-mode install — reinstall with sudo for full coverage"
		return
	}
	if degraded(s) {
		s.Overall = Degraded
		s.Note = degradedNote(s)
		return
	}

	s.Overall = Healthy
}

// noJobRunsYet reports whether every non-unavailable job has no run row.
func noJobRunsYet(jobs []JobHealth) bool {
	for _, j := range jobs {
		if j.Verdict == Unavailable {
			continue
		}
		if j.Status != "none" {
			return false
		}
	}
	return true
}

// degraded reports whether any applicable layer is in a non-healthy,
// non-down state worth flagging as DEGRADED.
func degraded(s *Snapshot) bool {
	if s.Mesh.Known && s.Mesh.RolesRunning < s.Mesh.RolesTotal {
		return true
	}
	if !s.Mesh.Known {
		return true // couldn't verify the engine (no sudo on a system install)
	}
	if s.Platform.Verdict == Degraded || s.Platform.Verdict == Down {
		return true
	}
	if s.Skills.Present < s.Skills.Total {
		return true
	}
	if s.Hosts.Verdict == Degraded {
		return true
	}
	if s.Pf.Enabled && (s.Pf.Verdict == Degraded || !s.Pf.Known) {
		return true
	}
	for _, j := range s.Jobs {
		if j.Verdict == Degraded || j.Verdict == Unavailable {
			return true
		}
	}
	return false
}

// degradedNote returns a short, token-free reason for a DEGRADED verdict,
// picking the most operator-relevant single cause.
func degradedNote(s *Snapshot) string {
	switch {
	case !s.Mesh.Known:
		return "engine state unknown — re-run with sudo"
	case s.Mesh.RolesRunning < s.Mesh.RolesTotal:
		return "engine partially running"
	case s.Platform.Verdict == Down:
		return "platform process not running"
	case s.Platform.Verdict == Degraded:
		return "platform version drift (desired not yet promoted)"
	case s.Skills.Present < s.Skills.Total:
		return "some Claude skill files are missing"
	case s.Pf.Enabled && !s.Pf.Known:
		return "packet filter state unknown — re-run with sudo"
	case s.Pf.Enabled && s.Pf.Verdict == Degraded:
		return "packet filter table is empty"
	default:
		return "a protection reported a recent failure"
	}
}
