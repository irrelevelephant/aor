package db

import (
	"database/sql"
	"fmt"

	"aor/afl/model"
)

const pathCols = `id, flow_id, path_type, name, sort_order, created_at, updated_at`

// CreatePath inserts a new path, generating a unique ID.
func (d *DB) CreatePath(flowID, pathType, name string, sortOrder int) (*model.Path, error) {
	id, err := d.generateUniqueIDForTable(3, "paths")
	if err != nil {
		return nil, err
	}

	_, err = d.Exec(`INSERT INTO paths (id, flow_id, path_type, name, sort_order) VALUES (?, ?, ?, ?, ?)`,
		id, flowID, pathType, name, sortOrder)
	if err != nil {
		return nil, fmt.Errorf("insert path: %w", err)
	}

	return d.GetPath(id)
}

// GetPath returns a single path by ID.
func (d *DB) GetPath(id string) (*model.Path, error) {
	var p model.Path
	err := d.QueryRow(`SELECT `+pathCols+` FROM paths WHERE id = ?`, id).
		Scan(&p.ID, &p.FlowID, &p.PathType, &p.Name, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("path not found")
		}
		return nil, fmt.Errorf("scan path: %w", err)
	}
	return &p, nil
}

// ListPaths returns all paths for a flow, ordered by sort_order.
func (d *DB) ListPaths(flowID string) ([]model.Path, error) {
	rows, err := d.Query(`SELECT `+pathCols+` FROM paths WHERE flow_id = ? ORDER BY sort_order ASC, created_at ASC`, flowID)
	if err != nil {
		return nil, fmt.Errorf("list paths: %w", err)
	}
	defer rows.Close()

	var paths []model.Path
	for rows.Next() {
		var p model.Path
		if err := rows.Scan(&p.ID, &p.FlowID, &p.PathType, &p.Name, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan path row: %w", err)
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// GetPathByName returns a path by flow ID and name.
func (d *DB) GetPathByName(flowID, name string) (*model.Path, error) {
	var p model.Path
	err := d.QueryRow(`SELECT `+pathCols+` FROM paths WHERE flow_id = ? AND name = ?`, flowID, name).
		Scan(&p.ID, &p.FlowID, &p.PathType, &p.Name, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan path: %w", err)
	}
	return &p, nil
}

// DeletePath deletes a path by ID. Cascades to steps and screenshots.
func (d *DB) DeletePath(id string) error {
	res, err := d.Exec(`DELETE FROM paths WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete path: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("path %s not found", id)
	}
	return nil
}
