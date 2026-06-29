// Package state is the platform's observed-state store: plugin inventory,
// job run history, locks, and events. It uses SQLite via the CGO-free
// modernc.org/sqlite driver so the platform cross-compiles trivially.
//
// Desired configuration lives in YAML (see core/config); only observed
// state belongs here.
package state

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (no CGO)
)

// DB owns the connection and exposes typed repositories.
type DB struct {
	sql *sql.DB

	Plugins *PluginRepo
	Jobs    *JobRepo
	Runs    *JobRunRepo
	Locks   *JobLockRepo
	Events  *EventRepo
}

// Open creates/opens the state DB at path, creating parent dirs and
// running pending migrations. Pass ":memory:" for tests.
func Open(path string) (*DB, error) {
	dsn := path
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create state dir: %w", err)
		}
		// WAL mode is the fix for the `status` flip: in the default
		// rollback-journal mode a writer holds an EXCLUSIVE lock during
		// commit, so the separate read-only `status` process contends with
		// the scheduler and its History query can hit SQLITE_BUSY. The status
		// reader maps that error to found=false → "no runs yet" → the OVERALL
		// verdict flips HEALTHY↔UNKNOWN. WAL lets a reader take a consistent
		// committed snapshot without ever blocking (or being blocked by) the
		// writer, so a job with >=1 recorded run is always read back as such.
		// busy_timeout still backs the rare writer-vs-checkpointer contention;
		// foreign_keys on for integrity.
		dsn = "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	}

	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite handles one writer at a time; a single conn avoids lock churn.
	sqldb.SetMaxOpenConns(1)

	if err := sqldb.Ping(); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := migrate(sqldb); err != nil {
		sqldb.Close()
		return nil, err
	}

	db := &DB{sql: sqldb}
	db.Plugins = &PluginRepo{db: sqldb}
	db.Jobs = &JobRepo{db: sqldb}
	db.Runs = &JobRunRepo{db: sqldb}
	db.Locks = &JobLockRepo{db: sqldb}
	db.Events = &EventRepo{db: sqldb}
	return db, nil
}

// OpenReadOnly opens an EXISTING state DB read-only: it creates no
// directory, runs no migrations, and never writes. It is the seam
// `platform status` uses so that (a) reading health never mutates
// protection state, and (b) a non-root user can read a user-owned DB
// without the migration write that the read-write Open performs.
//
// Returns an error if the file is missing or cannot be opened read-only
// (e.g. a root-owned system-install DB read without sudo) — the caller is
// expected to degrade to "history unavailable" rather than treat this as
// fatal.
func OpenReadOnly(path string) (*DB, error) {
	dsn := "file:" + path + "?mode=ro&_pragma=busy_timeout(2000)"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite ro: %w", err)
	}
	sqldb.SetMaxOpenConns(1)
	if err := sqldb.Ping(); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("ping sqlite ro: %w", err)
	}
	db := &DB{sql: sqldb}
	db.Plugins = &PluginRepo{db: sqldb}
	db.Jobs = &JobRepo{db: sqldb}
	db.Runs = &JobRunRepo{db: sqldb}
	db.Locks = &JobLockRepo{db: sqldb}
	db.Events = &EventRepo{db: sqldb}
	return db, nil
}

// Close releases the connection.
func (d *DB) Close() error { return d.sql.Close() }

// migrate applies pending migrations inside transactions, tracking
// applied versions in schema_migrations.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
        version    INTEGER PRIMARY KEY,
        applied_at TEXT NOT NULL
    )`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	var current int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
			m.version, now(),
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}
	return nil
}

// SchemaVersion returns the highest applied migration version.
func (d *DB) SchemaVersion() (int, error) {
	var v int
	err := d.sql.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}

// now is the canonical timestamp format for all TEXT time columns.
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
