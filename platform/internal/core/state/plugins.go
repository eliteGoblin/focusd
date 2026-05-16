package state

import (
	"database/sql"
	"fmt"
)

// PluginRepo manages the plugin inventory.
type PluginRepo struct{ db *sql.DB }

// Upsert inserts or replaces a plugin row, preserving discovered_at on
// re-discovery and refreshing last_seen_at.
func (r *PluginRepo) Upsert(p Plugin) error {
	ts := now()
	if p.DiscoveredAt == "" {
		p.DiscoveredAt = ts
	}
	p.LastSeenAt = ts
	_, err := r.db.Exec(`
INSERT INTO plugins (id,name,version,type,protocol_version,entrypoint,path,
    supported_os,supported_arch,required_privilege,run_as,enabled,
    discovered_at,last_seen_at,validation_status,validation_error)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,
    COALESCE((SELECT discovered_at FROM plugins WHERE id=?), ?),
    ?,?,?)
ON CONFLICT(id) DO UPDATE SET
    name=excluded.name, version=excluded.version, type=excluded.type,
    protocol_version=excluded.protocol_version, entrypoint=excluded.entrypoint,
    path=excluded.path, supported_os=excluded.supported_os,
    supported_arch=excluded.supported_arch,
    required_privilege=excluded.required_privilege, run_as=excluded.run_as,
    enabled=excluded.enabled, last_seen_at=excluded.last_seen_at,
    validation_status=excluded.validation_status,
    validation_error=excluded.validation_error`,
		p.ID, p.Name, p.Version, p.Type, p.ProtocolVersion, p.Entrypoint, p.Path,
		p.SupportedOS, p.SupportedArch, p.RequiredPrivilege, p.RunAs, b2i(p.Enabled),
		p.ID, p.DiscoveredAt,
		p.LastSeenAt, p.ValidationStatus, p.ValidationError)
	if err != nil {
		return fmt.Errorf("upsert plugin %s: %w", p.ID, err)
	}
	return nil
}

// Get returns a plugin by id, or sql.ErrNoRows if absent.
func (r *PluginRepo) Get(id string) (Plugin, error) {
	row := r.db.QueryRow(`SELECT id,name,version,type,protocol_version,entrypoint,
        path,supported_os,supported_arch,required_privilege,run_as,enabled,
        discovered_at,last_seen_at,validation_status,validation_error
        FROM plugins WHERE id=?`, id)
	return scanPlugin(row)
}

// List returns all plugins ordered by id.
func (r *PluginRepo) List() ([]Plugin, error) {
	rows, err := r.db.Query(`SELECT id,name,version,type,protocol_version,entrypoint,
        path,supported_os,supported_arch,required_privilege,run_as,enabled,
        discovered_at,last_seen_at,validation_status,validation_error
        FROM plugins ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list plugins: %w", err)
	}
	defer rows.Close()
	var out []Plugin
	for rows.Next() {
		p, err := scanPlugin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanPlugin(s scanner) (Plugin, error) {
	var p Plugin
	var enabled int
	err := s.Scan(&p.ID, &p.Name, &p.Version, &p.Type, &p.ProtocolVersion,
		&p.Entrypoint, &p.Path, &p.SupportedOS, &p.SupportedArch,
		&p.RequiredPrivilege, &p.RunAs, &enabled, &p.DiscoveredAt,
		&p.LastSeenAt, &p.ValidationStatus, &p.ValidationError)
	p.Enabled = enabled != 0
	return p, err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
