package db

import "fmt"

const schemaVersion = 1

// SchemaVersion returns the current schema version.
func SchemaVersion() int { return schemaVersion }

func (d *DB) migrate() error {
	// Create version table if not exists.
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	row := d.QueryRow(`SELECT version FROM schema_version LIMIT 1`)
	if err := row.Scan(&current); err != nil {
		// No row — first run.
		current = 0
	}

	if current >= schemaVersion {
		return nil
	}

	if current < 1 {
		if err := d.migrateV1(); err != nil {
			return fmt.Errorf("v1: %w", err)
		}
	}

	// Upsert version.
	if current == 0 {
		_, err := d.Exec(`INSERT INTO schema_version (version) VALUES (?)`, schemaVersion)
		if err != nil {
			return fmt.Errorf("insert version: %w", err)
		}
	} else {
		_, err := d.Exec(`UPDATE schema_version SET version = ?`, schemaVersion)
		if err != nil {
			return fmt.Errorf("update version: %w", err)
		}
	}

	return nil
}

func (d *DB) migrateV1() error {
	ddl := `
CREATE TABLE domains (
    id         TEXT PRIMARY KEY,
    slug       TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE flows (
    id         TEXT PRIMARY KEY,
    domain_id  TEXT NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    flow_id    TEXT NOT NULL,
    name       TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    UNIQUE(domain_id, flow_id)
);

CREATE TABLE paths (
    id         TEXT PRIMARY KEY,
    flow_id    TEXT NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
    path_type  TEXT NOT NULL CHECK(path_type IN ('happy','alternate','error')),
    name       TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE steps (
    id          TEXT PRIMARY KEY,
    path_id     TEXT NOT NULL REFERENCES paths(id) ON DELETE CASCADE,
    sort_order  INTEGER NOT NULL,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE screenshots (
    id             TEXT PRIMARY KEY,
    step_id        TEXT NOT NULL REFERENCES steps(id) ON DELETE CASCADE,
    platform       TEXT NOT NULL CHECK(platform IN ('web-desktop','web-mobile','ios','android')),
    filename       TEXT NOT NULL,
    stored_name    TEXT NOT NULL,
    mime_type      TEXT NOT NULL,
    size_bytes     INTEGER NOT NULL,
    capture_source TEXT NOT NULL DEFAULT '',
    captured_at    TEXT NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    UNIQUE(step_id, platform)
);

-- Indices on FK columns.
CREATE INDEX idx_flows_domain_id ON flows(domain_id);
CREATE INDEX idx_paths_flow_id ON paths(flow_id);
CREATE INDEX idx_steps_path_id ON steps(path_id);
CREATE INDEX idx_screenshots_step_id ON screenshots(step_id);

-- updated_at triggers.
CREATE TRIGGER domains_updated_at AFTER UPDATE ON domains BEGIN
    UPDATE domains SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = NEW.id;
END;

CREATE TRIGGER flows_updated_at AFTER UPDATE ON flows BEGIN
    UPDATE flows SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = NEW.id;
END;

CREATE TRIGGER paths_updated_at AFTER UPDATE ON paths BEGIN
    UPDATE paths SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = NEW.id;
END;

CREATE TRIGGER steps_updated_at AFTER UPDATE ON steps BEGIN
    UPDATE steps SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = NEW.id;
END;
`
	_, err := d.Exec(ddl)
	return err
}
