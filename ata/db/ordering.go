package db

import "fmt"

// Reorder sets a task's sort_order to the given position and shifts other tasks.
// If newStatus is non-empty and differs from the current status, the task is moved to that status.
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

	// Shift existing tasks at or after the target position in the target status group.
	_, err = tx.Exec(`UPDATE tasks SET sort_order = sort_order + 1 WHERE status = ? AND workspace = ? AND sort_order >= ? AND id != ?`,
		targetStatus, workspace, position, id)
	if err != nil {
		return fmt.Errorf("shift tasks: %w", err)
	}

	// Set the target task's position and status.
	_, err = tx.Exec(`UPDATE tasks SET sort_order = ?, status = ? WHERE id = ?`, position, targetStatus, id)
	if err != nil {
		return fmt.Errorf("set position: %w", err)
	}

	return tx.Commit()
}

// ReorderInEpic sets a child task's sort_order within its parent epic's children.
func (d *DB) ReorderInEpic(id string, position int, epicID string) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Verify the task belongs to this epic.
	var n int
	err = tx.QueryRow(`SELECT 1 FROM tasks WHERE id = ? AND epic_id = ?`, id, epicID).Scan(&n)
	if err != nil {
		return fmt.Errorf("task %s is not a child of epic %s", id, epicID)
	}

	// Shift existing children at or after the target position.
	_, err = tx.Exec(`UPDATE tasks SET sort_order = sort_order + 1 WHERE epic_id = ? AND sort_order >= ? AND id != ?`,
		epicID, position, id)
	if err != nil {
		return fmt.Errorf("shift tasks: %w", err)
	}

	// Set the target task's position.
	_, err = tx.Exec(`UPDATE tasks SET sort_order = ? WHERE id = ?`, position, id)
	if err != nil {
		return fmt.Errorf("set position: %w", err)
	}

	return tx.Commit()
}
