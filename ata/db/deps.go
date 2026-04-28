package db

import (
	"fmt"
	"strings"

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

// effectiveBlockedSubquery is a SQL subquery yielding task IDs blocked by
// either their own deps or any ancestor epic's deps. A task inherits its
// parent epic's blockers transitively. Use as `id [NOT] IN ` + effectiveBlockedSubquery.
//
// Implementation: seed with task IDs that have an unclosed direct dep, then
// propagate down the epic tree via epic_id. This is proportional to the deps
// set, which is typically much smaller than total tasks.
var effectiveBlockedSubquery = fmt.Sprintf(`(WITH RECURSIVE blocked(id, depth) AS (
	SELECT DISTINCT td.task_id, 0
	FROM task_deps td JOIN tasks dep ON dep.id = td.depends_on
	WHERE dep.status != 'closed'
	UNION ALL
	SELECT t.id, b.depth + 1 FROM tasks t JOIN blocked b ON t.epic_id = b.id WHERE b.depth < %d
)
SELECT DISTINCT id FROM blocked)`, maxEpicDepth)

// GetBlockers returns tasks that block taskID via direct dependencies only.
// Inherited blockers from ancestor epics are not included — use EffectiveBlockers
// for the "can this task start?" check.
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

// PropagateDeps copies blocking relationships: for every task T that depends
// on sourceID, T is made to also depend on newID. Cycles and duplicates are
// silently skipped. Returns the number of dependencies added.
func (d *DB) PropagateDeps(sourceID, newID string) (int, error) {
	blocking, err := d.GetBlocking(sourceID)
	if err != nil {
		return 0, fmt.Errorf("get blocking for %s: %w", sourceID, err)
	}

	added := 0
	for _, t := range blocking {
		if err := d.AddDep(t.ID, newID); err != nil {
			// Cycles and self-deps are expected — skip.
			// Real errors (missing task, DB failure) should propagate.
			msg := err.Error()
			if strings.Contains(msg, "cycle") || strings.Contains(msg, "depend on itself") {
				continue
			}
			return added, fmt.Errorf("propagate dep %s→%s: %w", t.ID, newID, err)
		}
		added++
	}
	return added, nil
}

// EffectiveBlockers returns unclosed tasks that block taskID, including
// blockers inherited from ancestor epics. Used to decide whether a task can
// be claimed.
func (d *DB) EffectiveBlockers(taskID string) ([]model.Task, error) {
	rows, err := d.Query(fmt.Sprintf(`
		WITH RECURSIVE ancestry(id, depth) AS (
			SELECT ?, 0
			UNION ALL
			SELECT t.epic_id, a.depth + 1
			FROM tasks t JOIN ancestry a ON t.id = a.id
			WHERE t.epic_id IS NOT NULL AND a.depth < %d
		)
		SELECT DISTINCT `+prefixCols("dep", taskCols)+`
		FROM ancestry a
		JOIN task_deps td ON td.task_id = a.id
		JOIN tasks dep ON dep.id = td.depends_on
		WHERE dep.status != 'closed'
		ORDER BY dep.created_at ASC
	`, maxEpicDepth), taskID)
	if err != nil {
		return nil, fmt.Errorf("effective blockers: %w", err)
	}
	defer rows.Close()
	return d.scanTasks(rows)
}

// BlockedTaskIDs returns a set of task IDs that have unclosed dependencies,
// either directly or via any ancestor epic. Used for bulk-annotating task lists.
func (d *DB) BlockedTaskIDs(taskIDs []string) (map[string]bool, error) {
	if len(taskIDs) == 0 {
		return make(map[string]bool), nil
	}

	ph, args := inPlaceholders(taskIDs)

	rows, err := d.Query(`SELECT id FROM tasks WHERE id IN (`+ph+`) AND id IN `+effectiveBlockedSubquery, args...)
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
