package state

import (
	"database/sql"
	"fmt"
	"time"
)

// JobLockRepo enforces no-overlap execution. A lock row existing (and
// not expired) means a run is in flight for that job.
type JobLockRepo struct{ db *sql.DB }

// TryAcquire atomically takes the lock for jobID. It returns false if a
// non-expired lock is already held. Expired locks are reclaimed (a prior
// run that crashed without releasing must not wedge the job forever).
func (r *JobLockRepo) TryAcquire(jobID string, runID int64, ttl time.Duration) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, fmt.Errorf("begin lock tx: %w", err)
	}
	defer tx.Rollback()

	var expiresAt string
	err = tx.QueryRow(`SELECT expires_at FROM job_locks WHERE job_id=?`, jobID).Scan(&expiresAt)
	switch {
	case err == sql.ErrNoRows:
		// no lock — fall through to acquire
	case err != nil:
		return false, fmt.Errorf("read lock %s: %w", jobID, err)
	default:
		exp, perr := time.Parse(time.RFC3339Nano, expiresAt)
		if perr == nil && nowTime().Before(exp) {
			return false, nil // still held
		}
		if _, err := tx.Exec(`DELETE FROM job_locks WHERE job_id=?`, jobID); err != nil {
			return false, fmt.Errorf("reclaim expired lock %s: %w", jobID, err)
		}
	}

	exp := nowTime().Add(ttl).Format(time.RFC3339Nano)
	if _, err := tx.Exec(
		`INSERT INTO job_locks (job_id,locked_at,run_id,expires_at) VALUES (?,?,?,?)`,
		jobID, now(), runID, exp,
	); err != nil {
		return false, fmt.Errorf("acquire lock %s: %w", jobID, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit lock %s: %w", jobID, err)
	}
	return true, nil
}

// Release drops the lock for jobID (idempotent).
func (r *JobLockRepo) Release(jobID string) error {
	if _, err := r.db.Exec(`DELETE FROM job_locks WHERE job_id=?`, jobID); err != nil {
		return fmt.Errorf("release lock %s: %w", jobID, err)
	}
	return nil
}

// Held reports whether a non-expired lock exists for jobID.
func (r *JobLockRepo) Held(jobID string) (bool, error) {
	var expiresAt string
	err := r.db.QueryRow(`SELECT expires_at FROM job_locks WHERE job_id=?`, jobID).Scan(&expiresAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	exp, perr := time.Parse(time.RFC3339Nano, expiresAt)
	if perr != nil {
		return true, nil // unparseable => treat as held (conservative)
	}
	return nowTime().Before(exp), nil
}
