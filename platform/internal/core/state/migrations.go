package state

// migration is one ordered, irreversible schema step. Versions must be
// contiguous and append-only; never edit a shipped migration.
type migration struct {
	version int
	sql     string
}

// migrations defines the full observed-state schema (spec §Suggested
// SQLite state tables). service_instances is created now but unused —
// the state layer is designed so service plugins can be added later
// without a structural rewrite.
var migrations = []migration{
	{
		version: 1,
		sql: `
CREATE TABLE plugins (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    version           TEXT NOT NULL,
    type              TEXT NOT NULL,
    protocol_version  TEXT NOT NULL,
    entrypoint        TEXT NOT NULL,
    path              TEXT NOT NULL,
    supported_os      TEXT NOT NULL,
    supported_arch    TEXT NOT NULL,
    required_privilege TEXT NOT NULL,
    run_as            TEXT NOT NULL,
    enabled           INTEGER NOT NULL DEFAULT 0,
    discovered_at     TEXT NOT NULL,
    last_seen_at      TEXT NOT NULL,
    validation_status TEXT NOT NULL,
    validation_error  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE jobs (
    id            TEXT PRIMARY KEY,
    plugin_id     TEXT NOT NULL,
    enabled       INTEGER NOT NULL DEFAULT 0,
    schedule      TEXT NOT NULL,
    timeout_ms    INTEGER NOT NULL DEFAULT 0,
    retry         INTEGER NOT NULL DEFAULT 0,
    allow_overlap INTEGER NOT NULL DEFAULT 0,
    config_hash   TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE TABLE job_runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id       TEXT NOT NULL,
    plugin_id    TEXT NOT NULL,
    plugin_version TEXT NOT NULL DEFAULT '',
    started_at   TEXT NOT NULL,
    ended_at     TEXT,
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    status       TEXT NOT NULL,
    exit_code    INTEGER NOT NULL DEFAULT 0,
    message      TEXT NOT NULL DEFAULT '',
    stdout_json  TEXT NOT NULL DEFAULT '',
    stderr_text  TEXT NOT NULL DEFAULT '',
    error_text   TEXT NOT NULL DEFAULT '',
    timed_out    INTEGER NOT NULL DEFAULT 0,
    triggered_by TEXT NOT NULL DEFAULT 'scheduler'
);
CREATE INDEX idx_job_runs_job ON job_runs(job_id, started_at);

CREATE TABLE job_locks (
    job_id     TEXT PRIMARY KEY,
    locked_at  TEXT NOT NULL,
    run_id     INTEGER NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE TABLE service_instances (
    id                   TEXT PRIMARY KEY,
    plugin_id            TEXT NOT NULL,
    enabled              INTEGER NOT NULL DEFAULT 0,
    status               TEXT NOT NULL DEFAULT 'stopped',
    pid                  INTEGER NOT NULL DEFAULT 0,
    started_at           TEXT,
    last_health_check_at TEXT,
    last_health_status   TEXT NOT NULL DEFAULT '',
    restart_count        INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE platform_events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp    TEXT NOT NULL,
    severity     TEXT NOT NULL,
    event_type   TEXT NOT NULL,
    message      TEXT NOT NULL,
    details_json TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_events_ts ON platform_events(timestamp);
`,
	},
}
