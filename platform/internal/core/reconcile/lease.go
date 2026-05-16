package reconcile

import (
	"time"

	"github.com/eliteGoblin/focusd/platform/internal/core/state"
)

// ReconcileLeaseKey is the single fixed key under which the staggered-
// upgrade lease lives. Reuses the proven, transactional, auto-expiring
// job_locks row pattern — no new table, KISS.
const ReconcileLeaseKey = "reconcile:upgrade"

// DBLease is a LeaseStore backed by the SQLite job_locks table.
type DBLease struct {
	Repo *state.JobLockRepo
}

// NewDBLease wires a LeaseStore onto the state DB.
func NewDBLease(db *state.DB) *DBLease { return &DBLease{Repo: db.Locks} }

// Acquire takes the lease if free/expired. Auto-expiry means a canary
// that crashes mid-upgrade cannot wedge future upgrades.
func (l *DBLease) Acquire(ttl time.Duration) (bool, error) {
	return l.Repo.TryAcquire(ReconcileLeaseKey, 0, ttl)
}

// Release frees the lease (idempotent; expiry also covers crashes).
func (l *DBLease) Release() error {
	return l.Repo.Release(ReconcileLeaseKey)
}

var _ LeaseStore = (*DBLease)(nil)
