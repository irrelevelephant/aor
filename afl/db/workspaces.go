package db

import (
	"database/sql"
	"fmt"

	"aor/afl/model"
)

// RegisterWorkspace registers a workspace path with an optional name.
func (d *DB) RegisterWorkspace(path, name string) error {
	_, err := d.Exec(
		`INSERT INTO workspaces (path, name) VALUES (?, ?)
		 ON CONFLICT(path) DO UPDATE SET name = CASE WHEN excluded.name != '' THEN excluded.name ELSE workspaces.name END`,
		path, name)
	if err != nil {
		return fmt.Errorf("register workspace: %w", err)
	}
	return nil
}

// UnregisterWorkspace removes a workspace registration.
func (d *DB) UnregisterWorkspace(path string) error {
	_, err := d.Exec(`DELETE FROM workspaces WHERE path = ?`, path)
	if err != nil {
		return fmt.Errorf("unregister workspace: %w", err)
	}
	return nil
}

// IsRegisteredWorkspace checks if a path is a registered workspace.
func (d *DB) IsRegisteredWorkspace(path string) (bool, error) {
	var exists int
	err := d.QueryRow(`SELECT 1 FROM workspaces WHERE path = ?`, path).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check workspace: %w", err)
	}
	return true, nil
}

// ResolveWorkspace resolves a name-or-path to a workspace path.
// Checks by name first, then by path.
func (d *DB) ResolveWorkspace(nameOrPath string) (string, error) {
	// Try name first.
	var path string
	err := d.QueryRow(`SELECT path FROM workspaces WHERE name = ?`, nameOrPath).Scan(&path)
	if err == nil {
		return path, nil
	}
	// Try path.
	err = d.QueryRow(`SELECT path FROM workspaces WHERE path = ?`, nameOrPath).Scan(&path)
	if err == nil {
		return path, nil
	}
	return "", fmt.Errorf("workspace %q not found", nameOrPath)
}

// GetWorkspace returns a workspace by path.
func (d *DB) GetWorkspace(path string) (*model.Workspace, error) {
	var ws model.Workspace
	err := d.QueryRow(`SELECT path, name, created_at FROM workspaces WHERE path = ?`, path).
		Scan(&ws.Path, &ws.Name, &ws.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &ws, nil
}

// ListRegisteredWorkspaces returns all registered workspaces.
func (d *DB) ListRegisteredWorkspaces() ([]model.Workspace, error) {
	rows, err := d.Query(`SELECT path, name, created_at FROM workspaces ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	defer rows.Close()

	var workspaces []model.Workspace
	for rows.Next() {
		var ws model.Workspace
		if err := rows.Scan(&ws.Path, &ws.Name, &ws.CreatedAt); err != nil {
			return nil, err
		}
		workspaces = append(workspaces, ws)
	}
	return workspaces, rows.Err()
}
