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
		// _busy_timeout avoids spurious "database is locked" under the
		// scheduler; foreign_keys on for integrity.
		dsn = "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
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
