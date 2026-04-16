package db

import (
	"fmt"
	"strings"
	"time"

	"aor/ata/model"
)

// ListClosedTasks returns closed tasks matching the given filters.
// If olderThan is zero, all closed tasks match regardless of age.
// Subtasks of closed epics are included automatically.
func (d *DB) ListClosedTasks(olderThan time.Duration) ([]model.Task, error) {
	query := `SELECT ` + taskCols + ` FROM tasks WHERE status = 'closed'`
	var args []any

	if olderThan > 0 {
		cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
		query += ` AND closed_at <= ?`
		args = append(args, cutoff)
	}

	query += ` ORDER BY closed_at ASC`

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list closed tasks: %w", err)
	}
	defer rows.Close()

	tasks, err := d.scanTasks(rows)
	if err != nil {
		return nil, err
	}

	// Also include closed subtasks of closed epics that matched.
	epicIDs := make([]string, 0)
	epicSet := make(map[string]bool)
	for _, t := range tasks {
		if t.IsEpic {
			epicIDs = append(epicIDs, t.ID)
			epicSet[t.ID] = true
		}
	}

	if len(epicIDs) > 0 {
		ph, phArgs := inPlaceholders(epicIDs)
		subQuery := `SELECT ` + taskCols + ` FROM tasks WHERE epic_id IN (` + ph + `) AND status = 'closed'`
		subRows, err := d.Query(subQuery, phArgs...)
		if err != nil {
			return nil, fmt.Errorf("list epic subtasks: %w", err)
		}
		defer subRows.Close()

		subtasks, err := d.scanTasks(subRows)
		if err != nil {
			return nil, err
		}

		// Add subtasks not already in the list.
		existing := make(map[string]bool, len(tasks))
		for _, t := range tasks {
			existing[t.ID] = true
		}
		for _, st := range subtasks {
			if !existing[st.ID] {
				tasks = append(tasks, st)
			}
		}
	}

	return tasks, nil
}

// GCClosedTasks deletes tasks by ID list in a transaction.
// FK cascades handle comments, deps, tags, and attachment rows.
// Returns the count of deleted tasks.
func (d *DB) GCClosedTasks(ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	tx, err := d.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Delete subtasks first (tasks with epic_id pointing to an ID in our list)
	// to avoid FK issues if cascades don't handle ordering.
	ph, args := inPlaceholders(ids)

	// Clear epic_id references for subtasks not in the delete set,
	// so we don't break open subtasks under a deleted epic.
	_, err = tx.Exec(`UPDATE tasks SET epic_id = NULL WHERE epic_id IN (`+ph+`) AND id NOT IN (`+ph+`)`,
		append(args, args...)...)
	if err != nil {
		return 0, fmt.Errorf("clear epic refs: %w", err)
	}

	// Delete the tasks. FK cascades handle comments, deps, tags, attachments.
	res, err := tx.Exec(`DELETE FROM tasks WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return 0, fmt.Errorf("delete tasks: %w", err)
	}

	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return int(deleted), nil
}

// AttachmentSummary holds aggregate attachment info for a task.
type AttachmentSummary struct {
	Count     int
	TotalSize int64
}

// GetAttachmentSummaries returns attachment count and total size per task ID.
func (d *DB) GetAttachmentSummaries(taskIDs []string) (map[string]AttachmentSummary, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}

	ph, args := inPlaceholders(taskIDs)
	rows, err := d.Query(
		`SELECT task_id, COUNT(*), COALESCE(SUM(size_bytes), 0) FROM attachments WHERE task_id IN (`+ph+`) GROUP BY task_id`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("attachment summaries: %w", err)
	}
	defer rows.Close()

	result := make(map[string]AttachmentSummary, len(taskIDs))
	for rows.Next() {
		var taskID string
		var s AttachmentSummary
		if err := rows.Scan(&taskID, &s.Count, &s.TotalSize); err != nil {
			return nil, fmt.Errorf("scan attachment summary: %w", err)
		}
		result[taskID] = s
	}
	return result, rows.Err()
}

// ParseDayDuration parses a duration string like "30d" into a time.Duration.
// Only the "Nd" (days) format is supported.
func ParseDayDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if !strings.HasSuffix(s, "d") {
		return 0, fmt.Errorf("invalid duration %q: must end with 'd' (e.g. 30d)", s)
	}
	numStr := strings.TrimSuffix(s, "d")
	var days int
	if _, err := fmt.Sscanf(numStr, "%d", &days); err != nil || days < 0 {
		return 0, fmt.Errorf("invalid duration %q: must be a non-negative number of days", s)
	}
	return time.Duration(days) * 24 * time.Hour, nil
}
