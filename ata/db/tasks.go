package db

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"aor/ata/model"
)

// SplitComma splits a comma-separated string into trimmed, non-empty parts.
func SplitComma(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// inPlaceholders builds SQL IN-clause placeholders and args from a string slice.
func inPlaceholders(ids []string) (string, []any) {
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	return strings.Join(ph, ","), args
}

// prefixCols adds a table alias prefix to each column in a comma-separated list.
func prefixCols(alias, cols string) string {
	parts := strings.Split(cols, ", ")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

const taskCols = `id, title, body, status, sort_order, epic_id, workspace, worktree, created_in, is_epic, spec, claimed_pid, claimed_at, closed_at, close_reason, created_at, updated_at`

// CreateTask inserts a new task, generating a unique ID.
func (d *DB) CreateTask(title, body, status, epicID, workspace, createdIn string) (*model.Task, error) {
	if status == "" {
		status = model.StatusQueue
	}

	id, err := d.generateUniqueID(3)
	if err != nil {
		return nil, err
	}

	tx, err := d.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Set sort_order to max+1 for the status group.
	var maxOrder int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(sort_order), 0) FROM tasks WHERE status = ? AND workspace = ?`, status, workspace).Scan(&maxOrder); err != nil {
		return nil, fmt.Errorf("query max sort_order: %w", err)
	}

	_, err = tx.Exec(`INSERT INTO tasks (id, title, body, status, sort_order, epic_id, workspace, created_in) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, title, body, status, maxOrder+1, nullStr(epicID), workspace, createdIn)
	if err != nil {
		return nil, fmt.Errorf("insert task: %w", err)
	}

	// Auto-promote parent to epic if it isn't one already.
	if epicID != "" {
		_, err = tx.Exec(`UPDATE tasks SET is_epic = 1 WHERE id = ? AND is_epic = 0`, epicID)
		if err != nil {
			return nil, fmt.Errorf("auto-promote epic: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return d.GetTask(id)
}

// GetTask returns a single task by ID.
func (d *DB) GetTask(id string) (*model.Task, error) {
	return d.scanTask(d.QueryRow(`SELECT ` + taskCols + ` FROM tasks WHERE id = ?`, id))
}

// GetTaskWithComments returns a task with its comments.
func (d *DB) GetTaskWithComments(id string) (*model.TaskWithComments, error) {
	task, err := d.GetTask(id)
	if err != nil {
		return nil, err
	}
	comments, err := d.ListComments(id)
	if err != nil {
		return nil, err
	}
	return &model.TaskWithComments{Task: *task, Comments: comments}, nil
}

// ListTasks returns tasks filtered by optional workspace, status, epic_id, tag (include), and excludeTag (exclude).
func (d *DB) ListTasks(workspace, status, epicID, tag, excludeTag string) ([]model.Task, error) {
	query := `SELECT ` + taskCols + ` FROM tasks WHERE 1=1`
	var args []any

	if workspace != "" {
		query += ` AND workspace = ?`
		args = append(args, workspace)
	}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	if epicID != "" {
		query += ` AND epic_id = ?`
		args = append(args, epicID)
	}
	if tag != "" {
		tags := SplitComma(tag)
		ph, tagArgs := inPlaceholders(tags)
		query += ` AND id IN (SELECT task_id FROM task_tags WHERE tag IN (` + ph + `))`
		args = append(args, tagArgs...)
	}
	if excludeTag != "" {
		tags := SplitComma(excludeTag)
		ph, tagArgs := inPlaceholders(tags)
		query += ` AND id NOT IN (SELECT task_id FROM task_tags WHERE tag IN (` + ph + `))`
		args = append(args, tagArgs...)
	}

	query += ` ORDER BY sort_order ASC, created_at ASC`

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	return d.scanTasks(rows)
}

// ReadyTasks returns queue tasks that are not blocked, optionally filtered by workspace, epic, and tag.
func (d *DB) ReadyTasks(workspace, epicID, tag string, limit int) ([]model.Task, error) {
	query := `SELECT ` + taskCols + ` FROM tasks WHERE status = 'queue' AND is_epic = 0`
	query += ` AND id NOT IN (SELECT td.task_id FROM task_deps td JOIN tasks dep ON dep.id = td.depends_on WHERE dep.status != 'closed')`
	var args []any

	if workspace != "" {
		query += ` AND workspace = ?`
		args = append(args, workspace)
	}
	if epicID != "" {
		query += ` AND epic_id = ?`
		args = append(args, epicID)
	}
	if tag != "" {
		query += ` AND id IN (SELECT task_id FROM task_tags WHERE tag = ?)`
		args = append(args, tag)
	}

	query += ` ORDER BY sort_order ASC, created_at ASC`

	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("ready tasks: %w", err)
	}
	defer rows.Close()

	return d.scanTasks(rows)
}

// ClaimTask marks a task as in_progress with the given PID.
// Rejects the claim if the task has unclosed dependencies.
func (d *DB) ClaimTask(id string, pid int) (*model.Task, error) {
	// Check for blockers before claiming.
	blockers, err := d.GetBlockers(id, true)
	if err != nil {
		return nil, fmt.Errorf("check blockers: %w", err)
	}
	if len(blockers) > 0 {
		ids := make([]string, len(blockers))
		for i, b := range blockers {
			ids[i] = b.ID
		}
		return nil, fmt.Errorf("task %s is blocked by: %s", id, strings.Join(ids, ", "))
	}

	now := time.Now().UTC().Format(time.RFC3339)

	res, err := d.Exec(`UPDATE tasks SET status = 'in_progress', claimed_pid = ?, claimed_at = ? WHERE id = ? AND status = 'queue'`,
		pid, now, id)
	if err != nil {
		return nil, fmt.Errorf("claim task: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		// Check if task exists and its status.
		task, err := d.GetTask(id)
		if err != nil {
			return nil, fmt.Errorf("task %s not found", id)
		}
		return nil, fmt.Errorf("task %s is %s, not queue", id, task.Status)
	}

	return d.GetTask(id)
}

// ForceClaimTask moves a task to in_progress from any non-closed status.
// The worktree parameter records where the agent is physically working.
func (d *DB) ForceClaimTask(id, worktree string) (*model.Task, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	pid := os.Getpid()

	res, err := d.Exec(`UPDATE tasks SET status = 'in_progress', claimed_pid = ?, claimed_at = ?, worktree = ? WHERE id = ? AND status != 'closed'`,
		pid, now, worktree, id)
	if err != nil {
		return nil, fmt.Errorf("force claim: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return nil, fmt.Errorf("task %s is closed or not found", id)
	}
	return d.GetTask(id)
}

// UnclaimTask resets a task from in_progress back to queue.
func (d *DB) UnclaimTask(id string) (*model.Task, error) {
	res, err := d.Exec(`UPDATE tasks SET status = 'queue', claimed_pid = NULL, claimed_at = NULL, worktree = '' WHERE id = ? AND status = 'in_progress'`, id)
	if err != nil {
		return nil, fmt.Errorf("unclaim task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		task, err := d.GetTask(id)
		if err != nil {
			return nil, fmt.Errorf("task %s not found", id)
		}
		return nil, fmt.Errorf("task %s is %s, not in_progress", id, task.Status)
	}
	return d.GetTask(id)
}

// UnclaimByWorkspace unclaims all in_progress tasks for a workspace.
func (d *DB) UnclaimByWorkspace(workspace string) ([]model.Task, error) {
	tx, err := d.Begin()
	if err != nil {
		return nil, fmt.Errorf("unclaim by workspace: %w", err)
	}
	defer tx.Rollback()

	// Read within the transaction for consistency.
	rows, err := tx.Query(`SELECT `+taskCols+` FROM tasks WHERE status = 'in_progress' AND workspace = ? ORDER BY sort_order ASC, created_at ASC`, workspace)
	if err != nil {
		return nil, fmt.Errorf("unclaim by workspace: %w", err)
	}
	defer rows.Close()
	tasks, err := scanTaskRows(rows)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, nil
	}

	_, err = tx.Exec(`UPDATE tasks SET status = 'queue', claimed_pid = NULL, claimed_at = NULL, worktree = '' WHERE status = 'in_progress' AND workspace = ?`, workspace)
	if err != nil {
		return nil, fmt.Errorf("unclaim by workspace: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("unclaim by workspace commit: %w", err)
	}
	return tasks, nil
}

// CloseTask closes a task with a reason.
// Epics cannot be closed while they have open subtasks.
func (d *DB) CloseTask(id, reason string) (*model.Task, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := d.Exec(`UPDATE tasks SET status = 'closed', close_reason = ?, closed_at = ?, claimed_pid = NULL, claimed_at = NULL, worktree = ''
		WHERE id = ? AND status != 'closed'
		AND NOT (is_epic = 1 AND EXISTS (
			SELECT 1 FROM tasks AS sub WHERE sub.epic_id = tasks.id AND sub.status != 'closed'
		))`,
		reason, now, id)
	if err != nil {
		return nil, fmt.Errorf("close task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		task, err := d.GetTask(id)
		if err != nil {
			return nil, fmt.Errorf("task %s not found", id)
		}
		if task.Status == model.StatusClosed {
			return nil, fmt.Errorf("task %s is already closed", id)
		}
		if task.IsEpic {
			var openCount int
			if err := d.QueryRow(`SELECT COUNT(*) FROM tasks WHERE epic_id = ? AND status != 'closed'`, id).Scan(&openCount); err != nil {
				return nil, fmt.Errorf("count open subtasks: %w", err)
			}
			if openCount > 0 {
				return nil, fmt.Errorf("cannot close epic %s: %d subtask(s) still open", id, openCount)
			}
		}
		return nil, fmt.Errorf("cannot close task %s (status: %s)", id, task.Status)
	}
	return d.GetTask(id)
}

// ReopenTask reopens a closed task back to backlog.
func (d *DB) ReopenTask(id string) (*model.Task, error) {
	res, err := d.Exec(`UPDATE tasks SET status = 'backlog', closed_at = NULL, close_reason = '' WHERE id = ? AND status = 'closed'`, id)
	if err != nil {
		return nil, fmt.Errorf("reopen task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return nil, fmt.Errorf("task %s is not closed", id)
	}
	return d.GetTask(id)
}

// PromoteToEpic promotes a task to an epic.
func (d *DB) PromoteToEpic(id, spec string) (*model.Task, error) {
	res, err := d.Exec(`UPDATE tasks SET is_epic = 1, spec = ? WHERE id = ? AND is_epic = 0`, spec, id)
	if err != nil {
		return nil, fmt.Errorf("promote: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		task, err := d.GetTask(id)
		if err != nil {
			return nil, fmt.Errorf("task %s not found", id)
		}
		if task.IsEpic {
			return nil, fmt.Errorf("task %s is already an epic", id)
		}
		return nil, fmt.Errorf("cannot promote task %s", id)
	}
	return d.GetTask(id)
}

// DemoteToTask converts an epic back to a regular task, if it has no children.
func (d *DB) DemoteToTask(id string) (*model.Task, error) {
	res, err := d.Exec(`UPDATE tasks SET is_epic = 0 WHERE id = ? AND is_epic = 1
		AND NOT EXISTS (SELECT 1 FROM tasks WHERE epic_id = ?)`, id, id)
	if err != nil {
		return nil, fmt.Errorf("demote: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		task, err := d.GetTask(id)
		if err != nil {
			return nil, fmt.Errorf("task %s not found", id)
		}
		if !task.IsEpic {
			return nil, fmt.Errorf("task %s is not an epic", id)
		}
		return nil, fmt.Errorf("cannot demote epic %s: it has sub-tasks", id)
	}
	return d.GetTask(id)
}

// UpdateTask updates task fields dynamically. nil pointer = no change, non-nil = set value.
func (d *DB) UpdateTask(id string, title, body, spec *string) (*model.Task, error) {
	var setClauses []string
	var args []any

	if title != nil {
		setClauses = append(setClauses, "title = ?")
		args = append(args, *title)
	}
	if body != nil {
		setClauses = append(setClauses, "body = ?")
		args = append(args, *body)
	}
	if spec != nil {
		setClauses = append(setClauses, "spec = ?")
		args = append(args, *spec)
	}

	if len(setClauses) == 0 {
		return nil, fmt.Errorf("no fields to update")
	}

	query := "UPDATE tasks SET " + strings.Join(setClauses, ", ") + " WHERE id = ?"
	args = append(args, id)

	res, err := d.Exec(query, args...)
	if err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return nil, fmt.Errorf("task %s not found", id)
	}
	return d.GetTask(id)
}

// ListTaskTree returns tasks as a tree: top-level items (standalone + epics) with children nested.
// Children whose parent epic is not in this status appear as top-level nodes.
func (d *DB) ListTaskTree(workspace, status, tag, excludeTag string) ([]model.TaskTreeNode, error) {
	tasks, err := d.ListTasks(workspace, status, "", tag, excludeTag)
	if err != nil {
		return nil, err
	}

	// Partition into top-level and children.
	var topLevel []model.Task
	childrenByEpic := make(map[string][]model.Task)
	for _, t := range tasks {
		if t.EpicID == "" {
			topLevel = append(topLevel, t)
		} else {
			childrenByEpic[t.EpicID] = append(childrenByEpic[t.EpicID], t)
		}
	}

	// Build result.
	var result []model.TaskTreeNode
	for _, t := range topLevel {
		node := model.TaskTreeNode{Task: t}
		if t.IsEpic {
			node.Children = childrenByEpic[t.ID]
			delete(childrenByEpic, t.ID)
		}
		result = append(result, node)
	}

	// Orphaned children (parent epic not in this status) become top-level.
	for _, children := range childrenByEpic {
		for _, c := range children {
			result = append(result, model.TaskTreeNode{Task: c})
		}
	}

	return result, nil
}

// SetEpicID moves a task into or out of an epic.
// If newEpicID is non-empty, the task becomes a child of that epic (auto-promoting it).
// If newEpicID is empty, the task is removed from its current epic.
func (d *DB) SetEpicID(taskID, newEpicID string) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if newEpicID != "" {
		// Auto-promote target to epic if needed.
		if _, err := tx.Exec(`UPDATE tasks SET is_epic = 1 WHERE id = ? AND is_epic = 0`, newEpicID); err != nil {
			return fmt.Errorf("auto-promote epic: %w", err)
		}

		// Place at end of epic's children in the task's current status.
		var maxOrder int
		err := tx.QueryRow(
			`SELECT COALESCE(MAX(sort_order), -1) FROM tasks WHERE epic_id = ?`,
			newEpicID).Scan(&maxOrder)
		if err != nil {
			return fmt.Errorf("query max sort_order: %w", err)
		}

		if _, err := tx.Exec(`UPDATE tasks SET epic_id = ?, sort_order = ? WHERE id = ?`,
			newEpicID, maxOrder+1, taskID); err != nil {
			return fmt.Errorf("set epic_id: %w", err)
		}
	} else {
		// Remove from epic: get workspace and status for placing at end of top-level.
		var status, workspace string
		if err := tx.QueryRow(`SELECT status, workspace FROM tasks WHERE id = ?`, taskID).Scan(&status, &workspace); err != nil {
			return fmt.Errorf("task %s not found", taskID)
		}

		var maxOrder int
		err := tx.QueryRow(
			`SELECT COALESCE(MAX(sort_order), -1) FROM tasks WHERE status = ? AND workspace = ? AND (epic_id IS NULL OR epic_id = '')`,
			status, workspace).Scan(&maxOrder)
		if err != nil {
			return fmt.Errorf("query max sort_order: %w", err)
		}

		if _, err := tx.Exec(`UPDATE tasks SET epic_id = NULL, sort_order = ? WHERE id = ?`,
			maxOrder+1, taskID); err != nil {
			return fmt.Errorf("clear epic_id: %w", err)
		}
	}

	return tx.Commit()
}

// EpicCloseEligible returns epics where all children are closed.
func (d *DB) EpicCloseEligible(workspace string) ([]model.Task, error) {
	query := `SELECT ` + prefixCols("e", taskCols) + `
		FROM tasks e
		WHERE e.is_epic = 1 AND e.status != 'closed'
		AND (SELECT COUNT(*) FROM tasks c WHERE c.epic_id = e.id) > 0
		AND (SELECT COUNT(*) FROM tasks c WHERE c.epic_id = e.id AND c.status != 'closed') = 0`
	var args []any
	if workspace != "" {
		query += ` AND e.workspace = ?`
		args = append(args, workspace)
	}

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("epic close eligible: %w", err)
	}
	defer rows.Close()

	return d.scanTasks(rows)
}

// EpicProgress returns progress counters for an epic's children.
func (d *DB) EpicProgress(epicID string) (*model.EpicProgress, error) {
	p := &model.EpicProgress{}
	rows, err := d.Query(`SELECT status, COUNT(*) FROM tasks WHERE epic_id = ? GROUP BY status`, epicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		p.Total += count
		switch status {
		case model.StatusClosed:
			p.Closed = count
		case model.StatusInProgress:
			p.InProg = count
		case model.StatusQueue:
			p.Queue = count
		case model.StatusBacklog:
			p.Backlog = count
		}
	}
	p.Open = p.Total - p.Closed
	return p, nil
}

// MoveTasks changes the status of tasks matching the filter criteria.
// It preserves sort_order values. If specific IDs are given, only those are moved.
// If fromStatus is given (and ids is empty), all tasks matching workspace+fromStatus are moved.
// Returns the moved tasks (with their pre-move state for display).
func (d *DB) MoveTasks(ids []string, fromStatus, toStatus, workspace string) ([]model.Task, error) {
	tx, err := d.Begin()
	if err != nil {
		return nil, fmt.Errorf("move tasks: %w", err)
	}
	defer tx.Rollback()

	var tasks []model.Task

	if len(ids) > 0 {
		// Move specific tasks by ID.
		ph, phArgs := inPlaceholders(ids)
		query := `SELECT ` + taskCols + ` FROM tasks WHERE id IN (` + ph + `) AND status != 'closed' ORDER BY sort_order ASC, created_at ASC`
		rows, qErr := tx.Query(query, phArgs...)
		if qErr != nil {
			return nil, fmt.Errorf("move tasks: %w", qErr)
		}
		defer rows.Close()
		tasks, err = scanTaskRows(rows)
		if err != nil {
			return nil, err
		}
		if len(tasks) == 0 {
			return nil, nil
		}
		// Update using the same ID set, re-checking status to avoid reopening closed tasks.
		args := []any{toStatus}
		args = append(args, phArgs...)
		_, err = tx.Exec(`UPDATE tasks SET status = ?, claimed_pid = NULL, claimed_at = NULL, worktree = '' WHERE id IN (`+ph+`) AND status != 'closed'`, args...)
	} else if fromStatus != "" {
		// Read and update within the same transaction.
		query := `SELECT ` + taskCols + ` FROM tasks WHERE status = ? AND workspace = ? ORDER BY sort_order ASC, created_at ASC`
		fromRows, qErr := tx.Query(query, fromStatus, workspace)
		if qErr != nil {
			return nil, fmt.Errorf("move tasks: %w", qErr)
		}
		defer fromRows.Close()
		tasks, err = scanTaskRows(fromRows)
		if err != nil {
			return nil, err
		}
		if len(tasks) == 0 {
			return nil, nil
		}
		_, err = tx.Exec(`UPDATE tasks SET status = ?, claimed_pid = NULL, claimed_at = NULL, worktree = '' WHERE workspace = ? AND status = ?`,
			toStatus, workspace, fromStatus)
	}

	if err != nil {
		return nil, fmt.Errorf("move tasks: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("move tasks commit: %w", err)
	}

	return tasks, nil
}

// RecoverStuckTasks finds in_progress tasks with dead PIDs and unclaims them.
func (d *DB) RecoverStuckTasks(workspace string) ([]model.Task, error) {
	tx, err := d.Begin()
	if err != nil {
		return nil, fmt.Errorf("recover stuck tasks: %w", err)
	}
	defer tx.Rollback()

	query := `SELECT ` + taskCols + ` FROM tasks WHERE status = 'in_progress' AND claimed_pid IS NOT NULL`
	var args []any
	if workspace != "" {
		query += ` AND workspace = ?`
		args = append(args, workspace)
	}

	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks, err := scanTaskRows(rows)
	if err != nil {
		return nil, err
	}

	// Collect IDs of tasks whose claiming process is dead.
	var deadIDs []string
	var recovered []model.Task
	for _, t := range tasks {
		if t.ClaimedPID > 0 && !isProcessAlive(t.ClaimedPID) {
			deadIDs = append(deadIDs, t.ID)
			t.Status = model.StatusQueue
			t.ClaimedPID = 0
			t.ClaimedAt = ""
			t.Worktree = ""
			recovered = append(recovered, t)
		}
	}

	// Batch update all dead-PID tasks in one statement.
	if len(deadIDs) > 0 {
		ph, phArgs := inPlaceholders(deadIDs)
		_, err = tx.Exec(`UPDATE tasks SET status = 'queue', claimed_pid = NULL, claimed_at = NULL, worktree = '' WHERE id IN (`+ph+`)`, phArgs...)
		if err != nil {
			return nil, fmt.Errorf("recover stuck tasks: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("recover stuck tasks commit: %w", err)
	}
	return recovered, nil
}

// generateUniqueID creates a unique ID for the tasks table.
func (d *DB) generateUniqueID(startLength int) (string, error) {
	return d.generateUniqueIDForTable(startLength, "tasks")
}

// generateUniqueIDForTable creates a unique ID, retrying on collision and escalating
// to longer IDs if needed. The table must have a TEXT PRIMARY KEY column named "id".
func (d *DB) generateUniqueIDForTable(startLength int, table string) (string, error) {
	const maxLength = 8
	query := `SELECT 1 FROM ` + table + ` WHERE id = ?`
	for length := startLength; length <= maxLength; length++ {
		for i := 0; i < 10; i++ {
			id, err := model.GenerateID(length)
			if err != nil {
				return "", err
			}
			var exists int
			err = d.QueryRow(query, id).Scan(&exists)
			if err == sql.ErrNoRows {
				return id, nil
			}
			if err != nil {
				return "", fmt.Errorf("check ID uniqueness: %w", err)
			}
		}
	}
	return "", fmt.Errorf("failed to generate unique ID after exhausting lengths up to %d", maxLength)
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanTaskRow(s scanner) (model.Task, error) {
	var t model.Task
	var epicID, claimedAt, closedAt sql.NullString
	var claimedPID sql.NullInt64

	err := s.Scan(&t.ID, &t.Title, &t.Body, &t.Status, &t.SortOrder,
		&epicID, &t.Workspace, &t.Worktree, &t.CreatedIn, &t.IsEpic, &t.Spec,
		&claimedPID, &claimedAt, &closedAt, &t.CloseReason,
		&t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return t, err
	}

	t.EpicID = epicID.String
	t.ClaimedPID = int(claimedPID.Int64)
	t.ClaimedAt = claimedAt.String
	t.ClosedAt = closedAt.String
	return t, nil
}

func (d *DB) scanTask(row *sql.Row) (*model.Task, error) {
	t, err := scanTaskRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("task not found")
		}
		return nil, fmt.Errorf("scan task: %w", err)
	}
	return &t, nil
}

// scanTaskRows scans rows into tasks without loading tags.
// Use this for bulk mutation responses where tags aren't needed.
func scanTaskRows(rows *sql.Rows) ([]model.Task, error) {
	var tasks []model.Task
	for rows.Next() {
		t, err := scanTaskRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task row: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (d *DB) scanTasks(rows *sql.Rows) ([]model.Task, error) {
	tasks, err := scanTaskRows(rows)
	if err != nil {
		return nil, err
	}
	return d.populateTags(tasks)
}

// populateTags batch-loads tags for a slice of tasks.
// Tag loading errors are non-fatal: tasks are returned without tags rather than failing.
func (d *DB) populateTags(tasks []model.Task) ([]model.Task, error) {
	if len(tasks) == 0 {
		return tasks, nil
	}
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	tagMap, err := d.GetTagsForTasks(ids)
	if err != nil {
		return tasks, nil // non-fatal: return tasks without tags
	}
	for i := range tasks {
		if tags, ok := tagMap[tasks[i].ID]; ok {
			tasks[i].Tags = tags
		}
	}
	return tasks, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// isProcessAlive checks if a PID is running.
func isProcessAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
