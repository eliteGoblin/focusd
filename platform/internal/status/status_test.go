package status

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// fixedRun builds a LastRunFn returning the same (status, age) for every
// job, with found controllable.
func runAt(status string, ago time.Duration, found bool) LastRunFn {
	return func(string) (string, time.Time, bool, error) {
		return status, now.Add(-ago), found, nil
	}
}

func TestCollect_VerdictMapping(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		lastRun LastRunFn
		want    Verdict
	}{
		{"disabled", false, runAt("ok", time.Minute, true), Disabled},
		{"no-run", true, runAt("", 0, false), Unknown},
		{"ok-recent", true, runAt("ok", 30*time.Second, true), Healthy},
		{"skipped-recent", true, runAt("skipped", time.Minute, true), Healthy},
		{"ok-stale", true, runAt("ok", 2*time.Hour, true), Degraded},
		{"failed", true, runAt("failed", time.Minute, true), Degraded},
		{"error", true, runAt("error", time.Minute, true), Degraded},
		{"timedout", true, runAt("timedout", time.Minute, true), Degraded},
		{"unavailable", true, runAt("unavailable", time.Minute, true), Unavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := Collect("system", []JobInput{{ID: "j", Enabled: c.enabled}}, c.lastRun, nil, nil, now)
			if got := rep.Jobs[0].Verdict; got != c.want {
				t.Fatalf("verdict = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCollect_DBErrorIsUnknownNotFatal(t *testing.T) {
	errFn := func(string) (string, time.Time, bool, error) {
		return "", time.Time{}, false, errFake
	}
	rep := Collect("system", []JobInput{{ID: "j", Enabled: true}}, errFn, nil, nil, now)
	if rep.Jobs[0].Verdict != Unknown {
		t.Fatalf("db error should map to Unknown, got %q", rep.Jobs[0].Verdict)
	}
}

func TestOverall_WorstWinsAndIgnoresDisabled(t *testing.T) {
	jobs := []JobInput{
		{ID: "a", Enabled: true},  // ok → Healthy
		{ID: "b", Enabled: false}, // Disabled (ignored)
		{ID: "c", Enabled: true},  // failed → Degraded
	}
	lastRun := func(id string) (string, time.Time, bool, error) {
		switch id {
		case "a":
			return "ok", now.Add(-time.Minute), true, nil
		case "c":
			return "failed", now.Add(-time.Minute), true, nil
		}
		return "", time.Time{}, false, nil
	}
	rep := Collect("system", jobs, lastRun, nil, nil, now)
	if rep.Overall != Degraded {
		t.Fatalf("overall = %q, want DEGRADED (worst of enabled)", rep.Overall)
	}
}

func TestOverall_AllDisabledIsUnknown(t *testing.T) {
	rep := Collect("user", []JobInput{{ID: "a", Enabled: false}}, runAt("", 0, false), nil, nil, now)
	if rep.Overall != Unknown {
		t.Fatalf("overall = %q, want UNKNOWN", rep.Overall)
	}
}

// TestOverall_UnavailableDegradesNotIgnored pins the BUG 3 fix: an
// "unavailable" job (reduced coverage, e.g. a system plugin under a user
// install) must DEGRADE the report — it must NOT be ignored like a
// config-disabled job. A user install with healthy + unavailable jobs reads
// DEGRADED, never HEALTHY/UNKNOWN.
func TestOverall_UnavailableDegradesNotIgnored(t *testing.T) {
	jobs := []JobInput{
		{ID: "a", Enabled: true}, // ok → Healthy
		{ID: "b", Enabled: true}, // unavailable → reduced coverage
	}
	lastRun := func(id string) (string, time.Time, bool, error) {
		switch id {
		case "a":
			return "ok", now.Add(-time.Minute), true, nil
		case "b":
			return "unavailable", now.Add(-time.Minute), true, nil
		}
		return "", time.Time{}, false, nil
	}
	rep := Collect("user", jobs, lastRun, nil, nil, now)
	if rep.Overall != Degraded {
		t.Fatalf("overall = %q, want DEGRADED (unavailable job must degrade, not be ignored)", rep.Overall)
	}
}

// TestOverall_DisabledVsUnavailable contrasts the two: an all-disabled set is
// UNKNOWN (deliberately off, ignored), whereas a single unavailable job in an
// otherwise-disabled set degrades the whole report.
func TestOverall_DisabledVsUnavailable(t *testing.T) {
	disabledOnly := Collect("user",
		[]JobInput{{ID: "a", Enabled: false}, {ID: "b", Enabled: false}},
		runAt("", 0, false), nil, nil, now)
	if disabledOnly.Overall != Unknown {
		t.Fatalf("all-disabled overall = %q, want UNKNOWN", disabledOnly.Overall)
	}

	withUnavailable := Collect("user",
		[]JobInput{{ID: "a", Enabled: false}, {ID: "b", Enabled: true}},
		runAt("unavailable", time.Minute, true), nil, nil, now)
	if withUnavailable.Overall != Degraded {
		t.Fatalf("has-unavailable overall = %q, want DEGRADED", withUnavailable.Overall)
	}
}

func TestAgeBuckets(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want AgeBucket
	}{
		{-time.Second, AgeUnder1m}, // clock skew
		{30 * time.Second, AgeUnder1m},
		{3 * time.Minute, AgeUnder5m},
		{30 * time.Minute, AgeUnder1h},
		{3 * time.Hour, AgeOver1h},
	}
	for _, c := range cases {
		if got := bucketAge(c.d); got != c.want {
			t.Errorf("bucketAge(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestRender_NoLeak is the redaction guard: the rendered report (text AND
// json) must never contain a path-like or label-like substring. The
// platform report carries only primitives by construction; this test fails
// loudly if a future field smuggles in a path/anchor.
func TestRender_NoLeak(t *testing.T) {
	rep := Collect("system", []JobInput{
		{ID: "dns-block-reconcile", Enabled: true},
		{ID: "network-block-reconcile", Enabled: true},
	}, runAt("ok", time.Minute, true), nil, nil, now)

	for _, render := range []func() string{
		func() string { var b bytes.Buffer; RenderText(rep, &b, false); return b.String() },
		func() string { var b bytes.Buffer; RenderJSON(rep, &b); return b.String() },
	} {
		out := render()
		for _, bad := range []string{"/Library/", "/Users/", "/var/", "Application Support", ".plist", "com.apple", "anchor"} {
			if strings.Contains(out, bad) {
				t.Errorf("rendered output leaked %q:\n%s", bad, out)
			}
		}
	}
}

func TestRenderJSON_Valid(t *testing.T) {
	rep := Collect("user", []JobInput{{ID: "j", Enabled: true}}, runAt("ok", time.Minute, true), nil, nil, now)
	var b bytes.Buffer
	RenderJSON(rep, &b)
	var back Report
	if err := json.Unmarshal(b.Bytes(), &back); err != nil {
		t.Fatalf("json round-trip failed: %v", err)
	}
	if back.Overall != Healthy {
		t.Fatalf("overall round-trip = %q, want HEALTHY", back.Overall)
	}
}

type fakeErr struct{}

func (fakeErr) Error() string { return "fake db error" }

var errFake = fakeErr{}

// noTamper is a TamperLookupFn reporting no integrity events.
func noTamper(string) (time.Time, int, bool) { return time.Time{}, 0, false }

// TestOverall_HealthyWithDisabledNoFalseDegraded is FEATURE 15 AC-6: all
// ENABLED protections healthy + one intentionally-DISABLED job (e.g.
// net-block off by default) must read HEALTHY overall — a disabled plugin
// must NOT drive DEGRADED.
func TestOverall_HealthyWithDisabledNoFalseDegraded(t *testing.T) {
	jobs := []JobInput{
		{ID: "dns-block-reconcile", Enabled: true},
		{ID: "kill-steam-reconcile", Enabled: true},
		{ID: "network-block-reconcile", Enabled: false}, // off by default
	}
	lastRun := func(id string) (string, time.Time, bool, error) {
		if id == "network-block-reconcile" {
			return "", time.Time{}, false, nil
		}
		return "ok", now.Add(-30 * time.Second), true, nil
	}
	rep := Collect("system", jobs, lastRun, noTamper, nil, now)
	if rep.Overall != Healthy {
		t.Fatalf("overall = %q, want HEALTHY (disabled job must not degrade)", rep.Overall)
	}
}

// TestTamper_OverOkRunIsTampered is FEATURE 15 AC-2 (false-green kill): a
// job with a clean "ok" run row but a tamper-repaired event NEWER than that
// run must read TAMPERED, never HEALTHY — a substitute that exits cleanly
// can no longer buy a green light.
func TestTamper_OverOkRunIsTampered(t *testing.T) {
	lastRun := runAt("ok", 5*time.Minute, true) // clean run 5m ago
	// Tamper event 1m ago (newer than the clean run), repaired twice.
	tamper := func(string) (time.Time, int, bool) {
		return now.Add(-time.Minute), 2, true
	}
	rep := Collect("system", []JobInput{{ID: "j", Enabled: true}}, lastRun, tamper, nil, now)
	if rep.Jobs[0].Verdict != Tampered {
		t.Fatalf("verdict = %q, want TAMPERED (tamper newer than ok run)", rep.Jobs[0].Verdict)
	}
	if rep.Jobs[0].TamperCount != 2 {
		t.Errorf("tamper count = %d, want 2", rep.Jobs[0].TamperCount)
	}
	if rep.Overall != Tampered {
		t.Fatalf("overall = %q, want TAMPERED (must dominate)", rep.Overall)
	}
}

// TestTamper_OlderThanCleanRunDoesNotFlip: a tamper that PREDATES the most
// recent clean run no longer flips the light (the repair already healed and
// a clean run followed). Verdict stays Healthy.
func TestTamper_OlderThanCleanRunDoesNotFlip(t *testing.T) {
	lastRun := runAt("ok", time.Minute, true) // clean run 1m ago
	tamper := func(string) (time.Time, int, bool) {
		return now.Add(-10 * time.Minute), 1, true // tamper 10m ago (older)
	}
	rep := Collect("system", []JobInput{{ID: "j", Enabled: true}}, lastRun, tamper, nil, now)
	if rep.Jobs[0].Verdict != Healthy {
		t.Fatalf("verdict = %q, want HEALTHY (tamper older than clean run)", rep.Jobs[0].Verdict)
	}
}

// TestSweepFailing_DegradesAndRenders is Fix 5 (anti-latent-failure): when
// the integrity sweep is failing, an otherwise all-healthy report must read
// DEGRADED and render a distinct "integrity sweep: FAILING" line — a wedged
// sweep can no longer hide behind a green status.
func TestSweepFailing_DegradesAndRenders(t *testing.T) {
	lastRun := runAt("ok", 30*time.Second, true) // every job healthy
	failing := func() bool { return true }
	rep := Collect("system", []JobInput{{ID: "j", Enabled: true}}, lastRun, nil, failing, now)

	if !rep.SweepFailing {
		t.Fatal("report should mark SweepFailing")
	}
	if rep.Overall != Degraded {
		t.Fatalf("overall = %q, want DEGRADED (failing sweep degrades a healthy report)", rep.Overall)
	}
	var b bytes.Buffer
	RenderText(rep, &b, false)
	if !strings.Contains(b.String(), "integrity sweep") || !strings.Contains(b.String(), "FAILING") {
		t.Errorf("rendered text missing the failing-sweep line:\n%s", b.String())
	}
}

// TestSweepFailing_FalseIsNoSignal: a non-failing sweep leaves the report
// untouched (no field, no degrade, no line).
func TestSweepFailing_FalseIsNoSignal(t *testing.T) {
	lastRun := runAt("ok", 30*time.Second, true)
	ok := func() bool { return false }
	rep := Collect("system", []JobInput{{ID: "j", Enabled: true}}, lastRun, nil, ok, now)
	if rep.SweepFailing {
		t.Error("a non-failing sweep must not set SweepFailing")
	}
	if rep.Overall != Healthy {
		t.Fatalf("overall = %q, want HEALTHY", rep.Overall)
	}
}

// TestSweepFailing_DoesNotMaskTampered: a failing sweep degrades, but must
// not down-rank a more-severe Tampered verdict.
func TestSweepFailing_DoesNotMaskTampered(t *testing.T) {
	lastRun := runAt("ok", 5*time.Minute, true)
	tamper := func(string) (time.Time, int, bool) { return now.Add(-time.Minute), 1, true }
	failing := func() bool { return true }
	rep := Collect("system", []JobInput{{ID: "j", Enabled: true}}, lastRun, tamper, failing, now)
	if rep.Overall != Tampered {
		t.Fatalf("overall = %q, want TAMPERED (must outrank a failing sweep)", rep.Overall)
	}
}

// TestTamper_OutsideWindowIgnored: a tamper older than tamperWindow does
// not flip the verdict.
func TestTamper_OutsideWindowIgnored(t *testing.T) {
	lastRun := runAt("ok", 30*time.Second, true)
	tamper := func(string) (time.Time, int, bool) {
		return now.Add(-25 * time.Hour), 1, true // >24h ago
	}
	rep := Collect("system", []JobInput{{ID: "j", Enabled: true}}, lastRun, tamper, nil, now)
	if rep.Jobs[0].Verdict != Healthy {
		t.Fatalf("verdict = %q, want HEALTHY (tamper outside window)", rep.Jobs[0].Verdict)
	}
}
