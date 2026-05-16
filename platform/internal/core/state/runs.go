package state

import (
	"database/sql"
	"fmt"
)

// JobRunRepo records job execution history.
type JobRunRepo struct{ db *sql.DB }

// Start inserts a run row in-progress and returns its id.
func (r *JobRunRepo) Start(jobID, pluginID, pluginVersion, triggeredBy string) (int64, error) {
	res, err := r.db.Exec(`
INSERT INTO job_runs (job_id,plugin_id,plugin_version,started_at,status,triggered_by)
VALUES (?,?,?,?,?,?)`,
		jobID, pluginID, pluginVersion, now(), "running", triggeredBy)
	if err != nil {
		return 0, fmt.Errorf("start run for job %s: %w", jobID, err)
	}
	return res.LastInsertId()
}

// Finish completes a run row with its terminal outcome.
func (r *JobRunRepo) Finish(run JobRun) error {
	_, err := r.db.Exec(`
UPDATE job_runs SET ended_at=?, duration_ms=?, status=?, exit_code=?,
    message=?, stdout_json=?, stderr_text=?, error_text=?, timed_out=?
WHERE id=?`,
		now(), run.DurationMS, run.Status, run.ExitCode, run.Message,
		run.StdoutJSON, run.StderrText, run.ErrorText, b2i(run.TimedOut), run.ID)
	if err != nil {
		return fmt.Errorf("finish run %d: %w", run.ID, err)
	}
	return nil
}

// RecordSkipped inserts a terminal skipped-run row (no-overlap).
func (r *JobRunRepo) RecordSkipped(jobID, pluginID, reason string) error {
	_, err := r.db.Exec(`
INSERT INTO job_runs (job_id,plugin_id,started_at,ended_at,status,message,triggered_by)
VALUES (?,?,?,?,?,?,?)`,
		jobID, pluginID, now(), now(), RunStatusSkipped, reason, "scheduler")
	return err
}

// History returns the most recent runs for a job, newest first.
func (r *JobRunRepo) History(jobID string, limit int) ([]JobRun, error) {
	rows, err := r.db.Query(`SELECT id,job_id,plugin_id,plugin_version,started_at,
        COALESCE(ended_at,''),duration_ms,status,exit_code,message,stdout_json,
        stderr_text,error_text,timed_out,triggered_by
        FROM job_runs WHERE job_id=? ORDER BY id DESC LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, fmt.Errorf("history for %s: %w", jobID, err)
	}
	defer rows.Close()
	var out []JobRun
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// LastByStatus returns the most recent run for a job with the given
// status, or sql.ErrNoRows.
func (r *JobRunRepo) LastByStatus(jobID, status string) (JobRun, error) {
	row := r.db.QueryRow(`SELECT id,job_id,plugin_id,plugin_version,started_at,
        COALESCE(ended_at,''),duration_ms,status,exit_code,message,stdout_json,
        stderr_text,error_text,timed_out,triggered_by
        FROM job_runs WHERE job_id=? AND status=? ORDER BY id DESC LIMIT 1`,
		jobID, status)
	return scanRun(row)
}

func scanRun(s scanner) (JobRun, error) {
	var run JobRun
	var timedOut int
	err := s.Scan(&run.ID, &run.JobID, &run.PluginID, &run.PluginVersion,
		&run.StartedAt, &run.EndedAt, &run.DurationMS, &run.Status,
		&run.ExitCode, &run.Message, &run.StdoutJSON, &run.StderrText,
		&run.ErrorText, &timedOut, &run.TriggeredBy)
	run.TimedOut = timedOut != 0
	return run, err
}
