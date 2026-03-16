package db

import (
	"database/sql"
	"fmt"

	"aor/ata/model"
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

// WorkspacesWithCounts returns workspace info with active task counts.
func (d *DB) WorkspacesWithCounts() ([]model.WorkspaceInfo, error) {
	rows, err := d.Query(`
		SELECT path, name, count FROM (
			SELECT w.path, w.name, COUNT(t.id) AS count
			FROM workspaces w
			LEFT JOIN tasks t ON t.workspace = w.path AND t.status != 'closed'
			GROUP BY w.path
			UNION
			SELECT t.workspace, COALESCE(w.name, ''), COUNT(*) AS count
			FROM tasks t
			LEFT JOIN workspaces w ON w.path = t.workspace
			WHERE t.status != 'closed' AND t.workspace NOT IN (SELECT path FROM workspaces)
			GROUP BY t.workspace
		) ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []model.WorkspaceInfo
	for rows.Next() {
		var wi model.WorkspaceInfo
		if err := rows.Scan(&wi.Path, &wi.Name, &wi.Count); err != nil {
			return nil, err
		}
		result = append(result, wi)
	}
	return result, rows.Err()
}

// WorkspaceTaskCounts returns open and closed task counts for a workspace.
func (d *DB) WorkspaceTaskCounts(path string) (open int, closed int, err error) {
	err = d.QueryRow(
		`SELECT COALESCE(SUM(CASE WHEN status != 'closed' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN status = 'closed' THEN 1 ELSE 0 END), 0)
		 FROM tasks WHERE workspace = ?`, path).Scan(&open, &closed)
	if err != nil {
		return 0, 0, fmt.Errorf("count workspace tasks: %w", err)
	}
	return open, closed, nil
}

// CleanWorkspace deletes all tasks (comments cascade via FK) and the workspace registration.
func (d *DB) CleanWorkspace(path string) (int64, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(`DELETE FROM tasks WHERE workspace = ?`, path)
	if err != nil {
		return 0, fmt.Errorf("delete tasks: %w", err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}

	if _, err := tx.Exec(`DELETE FROM workspaces WHERE path = ?`, path); err != nil {
		return 0, fmt.Errorf("delete workspace: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return deleted, nil
}
