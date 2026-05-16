package state

import (
	"database/sql"
	"fmt"
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
