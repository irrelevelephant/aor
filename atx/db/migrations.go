package db

import "fmt"

const schemaVersion = 2

func SchemaVersion() int { return schemaVersion }

func (d *DB) migrate() error {
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	if err := d.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&current); err != nil {
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
	if current < 2 {
		if err := d.migrateV2(); err != nil {
			return fmt.Errorf("v2: %w", err)
		}
	}

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
CREATE TABLE push_subscriptions (
    id           TEXT PRIMARY KEY,
    endpoint     TEXT NOT NULL UNIQUE,
    p256dh       TEXT NOT NULL,
    auth         TEXT NOT NULL,
    user_agent   TEXT,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL
);

CREATE TABLE notifications (
    id           INTEGER PRIMARY KEY,
    machine      TEXT NOT NULL,
    session      TEXT,
    window_index TEXT,
    window_name  TEXT,
    event        TEXT NOT NULL,
    message      TEXT,
    suppressed   INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL
);

CREATE INDEX notifications_created_at_idx ON notifications(created_at);
`
	_, err := d.Exec(ddl)
	return err
}

// migrateV2 adds a covering index for the unified machines view's
// per-window last-notified lookup, which scans+groups by
// (machine, window_index). Without it, the index page does a full table
// scan on every render and the cost grows with the notification log.
func (d *DB) migrateV2() error {
	_, err := d.Exec(`CREATE INDEX notifications_machine_window_idx ON notifications(machine, window_index, created_at)`)
	return err
}
