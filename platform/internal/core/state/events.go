package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Event types emitted by the plugin-integrity reconcile (FEATURE 15 /
// ADR-0019). They are stable strings the status layer queries on.
const (
	// EventTamperRepaired: an on-disk plugin binary did not match the
	// genuine embedded copy and was atomically restored, then run.
	EventTamperRepaired = "plugin_tamper_repaired"
	// EventIntegrityCheckFailed: the point-of-use integrity check errored
	// (e.g. disk unreadable); the runner did NOT exec a possibly-tampered
	// binary and will retry next tick.
	EventIntegrityCheckFailed = "plugin_integrity_check_failed"
	// EventIntegritySweepFailed: the periodic whole-bundle integrity sweep
	// errored. Recorded so a wedged sweep can't hide behind a green status.
	EventIntegritySweepFailed = "plugin_integrity_sweep_failed"
)

// EventRepo records platform-level events (skips, validation failures,
// lifecycle). details is an already-encoded JSON string or "".
type EventRepo struct{ db *sql.DB }

// Record appends one event.
func (r *EventRepo) Record(severity, eventType, message, detailsJSON string) error {
	if _, err := r.db.Exec(
		`INSERT INTO platform_events (timestamp,severity,event_type,message,details_json)
         VALUES (?,?,?,?,?)`,
		now(), severity, eventType, message, detailsJSON,
	); err != nil {
		return fmt.Errorf("record event %s: %w", eventType, err)
	}
	return nil
}

// Event is a platform_events row.
type Event struct {
	ID          int64
	Timestamp   string
	Severity    string
	EventType   string
	Message     string
	DetailsJSON string
}

// Recent returns the newest events, newest first.
func (r *EventRepo) Recent(limit int) ([]Event, error) {
	rows, err := r.db.Query(`SELECT id,timestamp,severity,event_type,message,details_json
        FROM platform_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent events: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.Severity, &e.EventType,
			&e.Message, &e.DetailsJSON); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RecordTamperRepaired records a detected-and-repaired plugin tamper as a
// security event keyed on the job id (the status layer's query key). The
// details carry ONLY sha PREFIXES — never a path, label, or anchor — so a
// weak-moment self reading the event learns nothing it could use to tear
// protection down (the want/got prefixes are non-actionable). pluginID is
// stored for human context; shaWantPrefix/shaGotPrefix are short hex
// prefixes of the genuine vs found content.
func (r *EventRepo) RecordTamperRepaired(jobID, pluginID, shaWantPrefix, shaGotPrefix string) error {
	details, _ := json.Marshal(map[string]string{
		"job_id":          jobID,
		"plugin_id":       pluginID,
		"want_sha_prefix": shaWantPrefix,
		"got_sha_prefix":  shaGotPrefix,
	})
	return r.Record(SeverityWarn, EventTamperRepaired,
		"plugin binary did not match genuine copy; restored", string(details))
}

// RecordIntegrityCheckFailed records that the point-of-use integrity check
// errored for a job, so the runner deliberately did NOT exec a possibly
// tampered binary. No path is recorded — only the job id and a short
// reason (the caller must pass a path-free message).
func (r *EventRepo) RecordIntegrityCheckFailed(jobID, pluginID, reason string) error {
	details, _ := json.Marshal(map[string]string{
		"job_id":    jobID,
		"plugin_id": pluginID,
		"reason":    reason,
	})
	return r.Record(SeverityError, EventIntegrityCheckFailed,
		"plugin integrity check failed; did not run", string(details))
}

// escapeLike escapes the SQL LIKE metacharacters (\, %, _) in s using `\`
// as the escape character, so an arbitrary jobID is matched literally
// rather than as a wildcard pattern. The backslash itself is escaped first
// so it isn't doubled by the later replacements.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// TamperSince returns, for one job, the time of its most-recent tamper-
// repaired event within `window` (counting back from now) and how many
// such events fell in that window. found=false means no tamper event in
// the window. It feeds status: a tamper newer than the last clean run must
// flip the job's verdict to Tampered so a repaired-then-clean run can never
// read as a plain "ok" (false-green close, AC-2).
func (r *EventRepo) TamperSince(jobID string, window time.Duration) (latest time.Time, count int, found bool, err error) {
	cutoff := time.Now().UTC().Add(-window).Format(time.RFC3339Nano)
	// platform_events.details_json carries the job id; match on it so a
	// tamper event is attributed to the right job. LIKE on the encoded
	// "job_id":"<id>" pair avoids a JSON extension dependency. The value is
	// anchored by its closing quote (so "j1" never matches a "j10" event),
	// and the key is anchored to a JSON object boundary ({ or ,) so a
	// future key like "old_job_id" can never cross-match.
	//
	// A jobID containing LIKE metacharacters (%, _, \) would otherwise be
	// treated as a wildcard and over-match other jobs' events. Escape them
	// and declare an ESCAPE char so the value is matched literally.
	val := `"job_id":"` + escapeLike(jobID) + `"`
	openPat := `%{` + val + `%`
	commaPat := `%,` + val + `%`
	rows, err := r.db.Query(
		`SELECT timestamp FROM platform_events
         WHERE event_type=? AND timestamp>=?
           AND (details_json LIKE ? ESCAPE '\' OR details_json LIKE ? ESCAPE '\')
         ORDER BY timestamp DESC`,
		EventTamperRepaired, cutoff, openPat, commaPat)
	if err != nil {
		return time.Time{}, 0, false, fmt.Errorf("tamper since for %s: %w", jobID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var ts string
		if err := rows.Scan(&ts); err != nil {
			return time.Time{}, 0, false, err
		}
		// Only count a row once its timestamp parses: count and found must
		// stay consistent. A row whose ts is unparseable is skipped entirely
		// (it can't set latest), so it must not inflate count either —
		// otherwise the caller could see found=false with count>0.
		t, perr := time.Parse(time.RFC3339Nano, ts)
		if perr != nil {
			continue
		}
		count++
		if !found {
			latest = t
			found = true
		}
	}
	return latest, count, found, rows.Err()
}

// SweepFailingSince reports whether the periodic whole-bundle integrity
// sweep is currently FAILING: it returns the time of the most-recent
// integrity_sweep_failed event within `window`, found=true if any.
//
// A healthy sweep runs @every 1m and records NO event, so a recovered
// sweep simply stops emitting failures and the last failure ages out of
// the window — "no subsequent success" is therefore the absence of a fresh
// failure, with no separate success event needed. This is the
// anti-latent-failure signal (Fix 5): a wedged sweep that leaves a tampered
// binary unrestored between scheduled runs surfaces as a degraded status
// line instead of hiding behind green.
func (r *EventRepo) SweepFailingSince(window time.Duration) (latest time.Time, found bool, err error) {
	cutoff := time.Now().UTC().Add(-window).Format(time.RFC3339Nano)
	row := r.db.QueryRow(
		`SELECT timestamp FROM platform_events
         WHERE event_type=? AND timestamp>=?
         ORDER BY timestamp DESC LIMIT 1`,
		EventIntegritySweepFailed, cutoff)
	var ts string
	switch err := row.Scan(&ts); {
	case err == sql.ErrNoRows:
		return time.Time{}, false, nil
	case err != nil:
		return time.Time{}, false, fmt.Errorf("sweep failing since: %w", err)
	}
	t, perr := time.Parse(time.RFC3339Nano, ts)
	if perr != nil {
		// A row exists but its timestamp is unparseable: the sweep DID fail
		// recently; surface it with a zero time rather than dropping it.
		return time.Time{}, true, nil
	}
	return t, true, nil
}
