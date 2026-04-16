package db

import (
	"database/sql"
	"fmt"

	"aor/afl/model"
)

const stepCols = `id, path_id, sort_order, name, description, created_at, updated_at`

// CreateStep inserts a new step, generating a unique ID.
func (d *DB) CreateStep(pathID, name, description string, sortOrder int) (*model.Step, error) {
	id, err := d.generateUniqueIDForTable(3, "steps")
	if err != nil {
		return nil, err
	}

	_, err = d.Exec(`INSERT INTO steps (id, path_id, sort_order, name, description) VALUES (?, ?, ?, ?, ?)`,
		id, pathID, sortOrder, name, description)
	if err != nil {
		return nil, fmt.Errorf("insert step: %w", err)
	}

	return d.GetStep(id)
}

// GetStep returns a single step by ID.
func (d *DB) GetStep(id string) (*model.Step, error) {
	var s model.Step
	err := d.QueryRow(`SELECT `+stepCols+` FROM steps WHERE id = ?`, id).
		Scan(&s.ID, &s.PathID, &s.SortOrder, &s.Name, &s.Description, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("step not found")
		}
		return nil, fmt.Errorf("scan step: %w", err)
	}
	return &s, nil
}

// ListSteps returns all steps for a path, ordered by sort_order.
func (d *DB) ListSteps(pathID string) ([]model.Step, error) {
	rows, err := d.Query(`SELECT `+stepCols+` FROM steps WHERE path_id = ? ORDER BY sort_order ASC, created_at ASC`, pathID)
	if err != nil {
		return nil, fmt.Errorf("list steps: %w", err)
	}
	defer rows.Close()

	var steps []model.Step
	for rows.Next() {
		var s model.Step
		if err := rows.Scan(&s.ID, &s.PathID, &s.SortOrder, &s.Name, &s.Description, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan step row: %w", err)
		}
		steps = append(steps, s)
	}
	return steps, rows.Err()
}

// GetStepByOrder returns a step by path ID and sort order.
func (d *DB) GetStepByOrder(pathID string, sortOrder int) (*model.Step, error) {
	var s model.Step
	err := d.QueryRow(`SELECT `+stepCols+` FROM steps WHERE path_id = ? AND sort_order = ?`, pathID, sortOrder).
		Scan(&s.ID, &s.PathID, &s.SortOrder, &s.Name, &s.Description, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan step: %w", err)
	}
	return &s, nil
}

// UpdateStep updates a step's name and/or description.
func (d *DB) UpdateStep(id string, name, description *string) (*model.Step, error) {
	var setClauses []string
	var args []any

	if name != nil {
		setClauses = append(setClauses, "name = ?")
		args = append(args, *name)
	}
	if description != nil {
		setClauses = append(setClauses, "description = ?")
		args = append(args, *description)
	}

	if len(setClauses) == 0 {
		return nil, fmt.Errorf("no fields to update")
	}

	query := "UPDATE steps SET " + joinStrings(setClauses, ", ") + " WHERE id = ?"
	args = append(args, id)

	res, err := d.Exec(query, args...)
	if err != nil {
		return nil, fmt.Errorf("update step: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return nil, fmt.Errorf("step %s not found", id)
	}
	return d.GetStep(id)
}

// DeleteStep deletes a step by ID. Cascades to screenshots.
func (d *DB) DeleteStep(id string) error {
	res, err := d.Exec(`DELETE FROM steps WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete step: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("step %s not found", id)
	}
	return nil
}

// joinStrings joins strings with a separator. Avoids importing strings package.
func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
}
