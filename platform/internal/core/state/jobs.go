package state

import (
	"database/sql"
	"fmt"
)

// JobRepo manages the observed projection of configured jobs.
type JobRepo struct{ db *sql.DB }

// Upsert inserts or updates a job row, preserving created_at.
func (r *JobRepo) Upsert(j Job) error {
	ts := now()
	if j.CreatedAt == "" {
		j.CreatedAt = ts
	}
	j.UpdatedAt = ts
	_, err := r.db.Exec(`
INSERT INTO jobs (id,plugin_id,enabled,schedule,timeout_ms,retry,allow_overlap,
    config_hash,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?,
    COALESCE((SELECT created_at FROM jobs WHERE id=?), ?), ?)
ON CONFLICT(id) DO UPDATE SET
    plugin_id=excluded.plugin_id, enabled=excluded.enabled,
    schedule=excluded.schedule, timeout_ms=excluded.timeout_ms,
    retry=excluded.retry, allow_overlap=excluded.allow_overlap,
    config_hash=excluded.config_hash, updated_at=excluded.updated_at`,
		j.ID, j.PluginID, b2i(j.Enabled), j.Schedule, j.TimeoutMS, j.Retry,
		b2i(j.AllowOverlap), j.ConfigHash, j.ID, j.CreatedAt, j.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert job %s: %w", j.ID, err)
	}
	return nil
}

// List returns all jobs ordered by id.
func (r *JobRepo) List() ([]Job, error) {
	rows, err := r.db.Query(`SELECT id,plugin_id,enabled,schedule,timeout_ms,
        retry,allow_overlap,config_hash,created_at,updated_at
        FROM jobs ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		var enabled, overlap int
		if err := rows.Scan(&j.ID, &j.PluginID, &enabled, &j.Schedule,
			&j.TimeoutMS, &j.Retry, &overlap, &j.ConfigHash,
			&j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		j.Enabled = enabled != 0
		j.AllowOverlap = overlap != 0
		out = append(out, j)
	}
	return out, rows.Err()
}

// Get returns one job by id, or sql.ErrNoRows.
func (r *JobRepo) Get(id string) (Job, error) {
	var j Job
	var enabled, overlap int
	err := r.db.QueryRow(`SELECT id,plugin_id,enabled,schedule,timeout_ms,
        retry,allow_overlap,config_hash,created_at,updated_at
        FROM jobs WHERE id=?`, id).Scan(&j.ID, &j.PluginID, &enabled,
		&j.Schedule, &j.TimeoutMS, &j.Retry, &overlap, &j.ConfigHash,
		&j.CreatedAt, &j.UpdatedAt)
	j.Enabled = enabled != 0
	j.AllowOverlap = overlap != 0
	return j, err
}
