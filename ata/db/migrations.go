package db

import "fmt"

const schemaVersion = 5

// SchemaVersion returns the current schema version for use in snapshot metadata.
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

	if current < 2 {
		if err := d.migrateV2(); err != nil {
			return fmt.Errorf("v2: %w", err)
		}
	}

	if current < 3 {
		if err := d.migrateV3(); err != nil {
			return fmt.Errorf("v3: %w", err)
		}
	}

	if current < 4 {
		if err := d.migrateV4(); err != nil {
			return fmt.Errorf("v4: %w", err)
		}
	}

	if current < 5 {
		if err := d.migrateV5(); err != nil {
			return fmt.Errorf("v5: %w", err)
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
CREATE TABLE tasks (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'backlog'
                CHECK (status IN ('backlog','queue','in_progress','closed')),
    sort_order  INTEGER NOT NULL DEFAULT 0,
    epic_id     TEXT REFERENCES tasks(id),
    workspace   TEXT NOT NULL DEFAULT '',
    is_epic     BOOLEAN NOT NULL DEFAULT 0,
    spec        TEXT NOT NULL DEFAULT '',
    claimed_pid INTEGER,
    claimed_at  TEXT,
    closed_at   TEXT,
    close_reason TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE comments (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    author     TEXT NOT NULL DEFAULT 'human',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX idx_tasks_workspace_status ON tasks(workspace, status);
CREATE INDEX idx_tasks_epic_id ON tasks(epic_id);
CREATE INDEX idx_tasks_status_sort ON tasks(status, sort_order);
CREATE INDEX idx_comments_task_id ON comments(task_id);

CREATE TRIGGER tasks_updated_at AFTER UPDATE ON tasks BEGIN
    UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = NEW.id;
END;
`
	_, err := d.Exec(ddl)
	return err
}

func (d *DB) migrateV2() error {
	ddl := `
CREATE TABLE IF NOT EXISTS workspaces (
    path       TEXT PRIMARY KEY,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

ALTER TABLE tasks ADD COLUMN worktree TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN created_in TEXT NOT NULL DEFAULT '';
`
	_, err := d.Exec(ddl)
	return err
}

func (d *DB) migrateV3() error {
	_, err := d.Exec(`ALTER TABLE workspaces ADD COLUMN name TEXT NOT NULL DEFAULT ''`)
	return err
}

func (d *DB) migrateV4() error {
	ddl := `
CREATE TABLE task_deps (
    task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    depends_on TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    PRIMARY KEY (task_id, depends_on),
    CHECK (task_id != depends_on)
);
CREATE INDEX idx_task_deps_depends_on ON task_deps(depends_on);
`
	_, err := d.Exec(ddl)
	return err
}

func (d *DB) migrateV5() error {
	ddl := `
CREATE TABLE task_tags (
    task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    tag        TEXT NOT NULL COLLATE NOCASE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    PRIMARY KEY (task_id, tag)
);
CREATE INDEX idx_task_tags_tag ON task_tags(tag);
`
	_, err := d.Exec(ddl)
	return err
}
