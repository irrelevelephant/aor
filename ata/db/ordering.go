package db

import (
	"database/sql"
	"fmt"
	"slices"
)

// scanIDs reads a single string column from rows into a slice.
func scanIDs(rows *sql.Rows) ([]string, error) {
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	return ids, rows.Err()
}

// reorderIDs removes id from ids, inserts it at position (clamped), and
// reassigns sort_order 0..n-1 for all items via the given transaction.
func reorderIDs(tx *sql.Tx, ids []string, id string, position int) error {
	if i := slices.Index(ids, id); i >= 0 {
		ids = slices.Delete(ids, i, i+1)
	}

	position = max(0, min(position, len(ids)))
	ids = slices.Insert(ids, position, id)

	for i, tid := range ids {
		if _, err := tx.Exec(`UPDATE tasks SET sort_order = ? WHERE id = ?`, i, tid); err != nil {
			return fmt.Errorf("reassign sort_order: %w", err)
		}
	}
	return nil
}

// Reorder sets a top-level task's position within its status+workspace group.
// If newStatus is non-empty and differs from the current status, the task is moved to that status.
// Uses an array-based approach: read all IDs, remove+insert, reassign 0..n-1.
func (d *DB) Reorder(id string, position int, newStatus string) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Get the task's current status and workspace.
	var status, workspace string
	err = tx.QueryRow(`SELECT status, workspace FROM tasks WHERE id = ?`, id).Scan(&status, &workspace)
	if err != nil {
		return fmt.Errorf("task %s not found", id)
	}

	// Determine target status.
	targetStatus := status
	if newStatus != "" && newStatus != status {
		targetStatus = newStatus
	}

	// Query ordered IDs of top-level items in the target status+workspace.
	rows, err := tx.Query(
		`SELECT id FROM tasks WHERE status = ? AND workspace = ? AND (epic_id IS NULL OR epic_id = '') ORDER BY sort_order ASC, created_at ASC`,
		targetStatus, workspace)
	if err != nil {
		return fmt.Errorf("query top-level ids: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return fmt.Errorf("scan top-level ids: %w", err)
	}

	// If changing status, also need to remove from the old status list.
	if targetStatus != status {
		oldRows, err := tx.Query(
			`SELECT id FROM tasks WHERE status = ? AND workspace = ? AND (epic_id IS NULL OR epic_id = '') ORDER BY sort_order ASC, created_at ASC`,
			status, workspace)
		if err != nil {
			return fmt.Errorf("query old status ids: %w", err)
		}
		oldIDs, err := scanIDs(oldRows)
		if err != nil {
			return fmt.Errorf("scan old status ids: %w", err)
		}

		// Remove from old list and reassign.
		if err := reorderIDs(tx, oldIDs, id, len(oldIDs)); err != nil {
			return fmt.Errorf("reassign old: %w", err)
		}
	}

	if err := reorderIDs(tx, ids, id, position); err != nil {
		return err
	}

	// Update the moved task's status if changed.
	if targetStatus != status {
		if _, err := tx.Exec(`UPDATE tasks SET status = ? WHERE id = ?`, targetStatus, id); err != nil {
			return fmt.Errorf("update status: %w", err)
		}
	}

	return tx.Commit()
}

// ReorderInEpic sets a child task's sort_order within its parent epic's children.
// Uses an array-based approach: read all IDs, remove+insert, reassign 0..n-1.
func (d *DB) ReorderInEpic(id string, position int, epicID string) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Query ordered IDs of children for the given epic.
	rows, err := tx.Query(
		`SELECT id FROM tasks WHERE epic_id = ? ORDER BY sort_order ASC, created_at ASC`,
		epicID)
	if err != nil {
		return fmt.Errorf("query epic children: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return fmt.Errorf("scan epic children: %w", err)
	}

	if err := reorderIDs(tx, ids, id, position); err != nil {
		return err
	}

	return tx.Commit()
}
