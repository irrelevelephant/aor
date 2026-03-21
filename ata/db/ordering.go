package db

import (
	"database/sql"
	"fmt"
	"slices"
)

// topLevelFilter returns a SQL WHERE fragment (and bind args) that matches
// tasks considered "top-level roots" in the given status+workspace — those
// with no epic_id, or whose epic_id references a parent not present in the
// same status+workspace (i.e. orphans whose parent was closed/moved).
// This matches the logic in ListTaskTree that determines root nodes.
func topLevelFilter(status, workspace string) (string, []any) {
	return `(epic_id IS NULL OR epic_id = '' OR NOT EXISTS (SELECT 1 FROM tasks p WHERE p.id = tasks.epic_id AND p.status = ? AND p.workspace = ?))`,
		[]any{status, workspace}
}

// queryTopLevelIDs returns the ordered IDs of top-level root tasks (including
// orphans) in the given status+workspace.
func queryTopLevelIDs(tx *sql.Tx, status, workspace string) ([]string, error) {
	frag, fArgs := topLevelFilter(status, workspace)
	args := []any{status, workspace}
	args = append(args, fArgs...)
	rows, err := tx.Query(
		`SELECT id FROM tasks WHERE status = ? AND workspace = ? AND `+frag+` ORDER BY sort_order ASC, created_at ASC`,
		args...)
	if err != nil {
		return nil, err
	}
	return scanIDs(rows)
}

// ReorderOpts specifies how to determine the target position for a reorder.
// Exactly one of Position (>= 0), Top, Bottom, Before, or After should be set.
type ReorderOpts struct {
	Position int    // Target position (0-based), -1 means unset
	Top      bool   // Move to position 0
	Bottom   bool   // Move to end of list
	Before   string // Place before this task ID
	After    string // Place after this task ID
}

// resolvePosition converts ReorderOpts into a numeric position given the ordered
// list of sibling IDs. The task being reordered (id) should already be in ids
// (or not, if it's being inserted from another list).
func resolvePosition(ids []string, id string, opts ReorderOpts) (int, error) {
	if opts.Top {
		return 0, nil
	}
	if opts.Bottom {
		return len(ids), nil
	}
	if opts.Before != "" {
		// Find index of the reference task (in the list without the moving task).
		filtered := withoutID(ids, id)
		idx := slices.Index(filtered, opts.Before)
		if idx < 0 {
			return 0, fmt.Errorf("task %s not found in sibling list", opts.Before)
		}
		return idx, nil
	}
	if opts.After != "" {
		filtered := withoutID(ids, id)
		idx := slices.Index(filtered, opts.After)
		if idx < 0 {
			return 0, fmt.Errorf("task %s not found in sibling list", opts.After)
		}
		return idx + 1, nil
	}
	// opts.Position >= 0
	return opts.Position, nil
}

// withoutID returns a copy of ids with the given id removed.
func withoutID(ids []string, id string) []string {
	out := make([]string, 0, len(ids))
	for _, v := range ids {
		if v != id {
			out = append(out, v)
		}
	}
	return out
}

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

// nextEpicChildOrder returns the sort_order to assign to a new child of the
// given epic, placing it after all non-closed siblings.
func nextEpicChildOrder(tx *sql.Tx, epicID string) (int, error) {
	var maxOrder int
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(sort_order), -1) FROM tasks WHERE epic_id = ? AND status != 'closed'`,
		epicID).Scan(&maxOrder); err != nil {
		return 0, fmt.Errorf("query max sort_order: %w", err)
	}
	return maxOrder + 1, nil
}

// nextTopLevelOrder returns the sort_order to assign to a new top-level task
// in the given status+workspace group, placing it after all existing siblings.
func nextTopLevelOrder(tx *sql.Tx, status, workspace string) (int, error) {
	frag, fArgs := topLevelFilter(status, workspace)
	var maxOrder int
	args := []any{status, workspace}
	args = append(args, fArgs...)
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(sort_order), -1) FROM tasks WHERE status = ? AND workspace = ? AND `+frag,
		args...).Scan(&maxOrder); err != nil {
		return 0, fmt.Errorf("query max sort_order: %w", err)
	}
	return maxOrder + 1, nil
}

// recompactSiblings reassigns sort_order 0..n-1 to the siblings of a task
// that was just removed from a group (e.g. by closing). If epicID is non-null
// and non-empty, recompacts the epic's non-closed children; otherwise
// recompacts the top-level tasks in the given status+workspace.
func recompactSiblings(tx *sql.Tx, epicID sql.NullString, status, workspace string) error {
	var ids []string
	var err error
	if epicID.Valid && epicID.String != "" {
		var rows *sql.Rows
		rows, err = tx.Query(
			`SELECT id FROM tasks WHERE epic_id = ? AND status != 'closed' ORDER BY sort_order ASC, created_at ASC`,
			epicID.String)
		if err != nil {
			return fmt.Errorf("recompact query: %w", err)
		}
		ids, err = scanIDs(rows)
	} else {
		ids, err = queryTopLevelIDs(tx, status, workspace)
	}
	if err != nil {
		return fmt.Errorf("recompact query: %w", err)
	}
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE tasks SET sort_order = ? WHERE id = ?`, i, id); err != nil {
			return fmt.Errorf("recompact update: %w", err)
		}
	}
	return nil
}

// Reorder sets a top-level task's position within its status+workspace group.
// If newStatus is non-empty and differs from the current status, the task is moved to that status.
func (d *DB) Reorder(id string, position int, newStatus string) error {
	return d.ReorderOpt(id, newStatus, ReorderOpts{Position: position})
}

// ReorderOpt is like Reorder but accepts ReorderOpts for flexible positioning.
func (d *DB) ReorderOpt(id string, newStatus string, opts ReorderOpts) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var status, workspace string
	err = tx.QueryRow(`SELECT status, workspace FROM tasks WHERE id = ?`, id).Scan(&status, &workspace)
	if err != nil {
		return fmt.Errorf("task %s not found", id)
	}

	targetStatus := status
	if newStatus != "" && newStatus != status {
		targetStatus = newStatus
	}

	ids, err := queryTopLevelIDs(tx, targetStatus, workspace)
	if err != nil {
		return fmt.Errorf("query top-level ids: %w", err)
	}

	position, err := resolvePosition(ids, id, opts)
	if err != nil {
		return err
	}

	if targetStatus != status {
		oldIDs, err := queryTopLevelIDs(tx, status, workspace)
		if err != nil {
			return fmt.Errorf("query old status ids: %w", err)
		}

		if err := reorderIDs(tx, oldIDs, id, len(oldIDs)); err != nil {
			return fmt.Errorf("reassign old: %w", err)
		}
	}

	if err := reorderIDs(tx, ids, id, position); err != nil {
		return err
	}

	if targetStatus != status {
		if _, err := tx.Exec(`UPDATE tasks SET status = ? WHERE id = ?`, targetStatus, id); err != nil {
			return fmt.Errorf("update status: %w", err)
		}
	}

	return tx.Commit()
}

// ReorderInEpic sets a child task's sort_order within its parent epic's children.
func (d *DB) ReorderInEpic(id string, position int, epicID string) error {
	return d.ReorderInEpicOpts(id, epicID, ReorderOpts{Position: position})
}

// ReorderInEpicOpts is like ReorderInEpic but accepts ReorderOpts for flexible positioning.
func (d *DB) ReorderInEpicOpts(id string, epicID string, opts ReorderOpts) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

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

	position, err := resolvePosition(ids, id, opts)
	if err != nil {
		return err
	}

	if err := reorderIDs(tx, ids, id, position); err != nil {
		return err
	}

	return tx.Commit()
}
