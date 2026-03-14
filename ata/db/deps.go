package db

import (
	"fmt"

	"aor/ata/model"
)

// AddDep creates a dependency: taskID depends on dependsOnID.
// Returns an error if the dependency would create a cycle.
func (d *DB) AddDep(taskID, dependsOnID string) error {
	if taskID == dependsOnID {
		return fmt.Errorf("task cannot depend on itself")
	}

	// Verify both tasks exist.
	if _, err := d.GetTask(taskID); err != nil {
		return fmt.Errorf("task %s not found", taskID)
	}
	if _, err := d.GetTask(dependsOnID); err != nil {
		return fmt.Errorf("task %s not found", dependsOnID)
	}

	// Cycle detection: check if taskID is reachable from dependsOnID's deps.
	var cycle int
	err := d.QueryRow(`
		WITH RECURSIVE chain(id) AS (
			SELECT depends_on FROM task_deps WHERE task_id = ?
			UNION
			SELECT td.depends_on FROM task_deps td JOIN chain c ON td.task_id = c.id
		)
		SELECT 1 FROM chain WHERE id = ?
	`, dependsOnID, taskID).Scan(&cycle)
	if err == nil {
		return fmt.Errorf("cannot add dependency: would create a cycle")
	}

	_, err = d.Exec(`INSERT OR IGNORE INTO task_deps (task_id, depends_on) VALUES (?, ?)`, taskID, dependsOnID)
	if err != nil {
		return fmt.Errorf("add dep: %w", err)
	}
	return nil
}

// RemoveDep removes a dependency.
func (d *DB) RemoveDep(taskID, dependsOnID string) error {
	res, err := d.Exec(`DELETE FROM task_deps WHERE task_id = ? AND depends_on = ?`, taskID, dependsOnID)
	if err != nil {
		return fmt.Errorf("remove dep: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("dependency not found: %s -> %s", taskID, dependsOnID)
	}
	return nil
}

// GetBlockers returns tasks that block taskID.
// If activeOnly is true, only unclosed blockers are returned.
func (d *DB) GetBlockers(taskID string, activeOnly bool) ([]model.Task, error) {
	query := `SELECT ` + prefixCols("t", taskCols) + `
		FROM task_deps td
		JOIN tasks t ON t.id = td.depends_on
		WHERE td.task_id = ?`
	if activeOnly {
		query += ` AND t.status != 'closed'`
	}
	query += ` ORDER BY t.created_at ASC`

	rows, err := d.Query(query, taskID)
	if err != nil {
		return nil, fmt.Errorf("get blockers: %w", err)
	}
	defer rows.Close()
	return d.scanTasks(rows)
}

// GetBlocking returns tasks that depend on taskID.
func (d *DB) GetBlocking(taskID string) ([]model.Task, error) {
	rows, err := d.Query(`
		SELECT `+prefixCols("t", taskCols)+`
		FROM task_deps td
		JOIN tasks t ON t.id = td.task_id
		WHERE td.depends_on = ?
		ORDER BY t.created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("get blocking: %w", err)
	}
	defer rows.Close()
	return d.scanTasks(rows)
}

// BlockedTaskIDs returns a set of task IDs that have unclosed dependencies.
// Used for bulk-annotating task lists.
func (d *DB) BlockedTaskIDs(taskIDs []string) (map[string]bool, error) {
	if len(taskIDs) == 0 {
		return make(map[string]bool), nil
	}

	ph, args := inPlaceholders(taskIDs)

	rows, err := d.Query(`
		SELECT DISTINCT td.task_id
		FROM task_deps td
		JOIN tasks t ON t.id = td.depends_on
		WHERE td.task_id IN (`+ph+`) AND t.status != 'closed'
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}
