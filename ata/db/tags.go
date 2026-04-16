package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// scanStrings scans single-column string rows into a slice.
func scanStrings(rows *sql.Rows) ([]string, error) {
	var result []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

// AddTag adds a tag to a task. No-op if the tag already exists.
func (d *DB) AddTag(taskID, tag string) error {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return fmt.Errorf("tag cannot be empty")
	}
	_, err := d.Exec(`INSERT OR IGNORE INTO task_tags (task_id, tag) VALUES (?, ?)`, taskID, tag)
	if err != nil {
		return fmt.Errorf("add tag: %w", err)
	}
	return nil
}

// RemoveTag removes a tag from a task.
func (d *DB) RemoveTag(taskID, tag string) error {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return fmt.Errorf("tag cannot be empty")
	}
	res, err := d.Exec(`DELETE FROM task_tags WHERE task_id = ? AND tag = ?`, taskID, tag)
	if err != nil {
		return fmt.Errorf("remove tag: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("tag %q not found on task %s", tag, taskID)
	}
	return nil
}

// GetTags returns all tags for a single task, sorted alphabetically.
func (d *DB) GetTags(taskID string) ([]string, error) {
	rows, err := d.Query(`SELECT tag FROM task_tags WHERE task_id = ? ORDER BY tag`, taskID)
	if err != nil {
		return nil, fmt.Errorf("get tags: %w", err)
	}
	defer rows.Close()
	return scanStrings(rows)
}

// GetTagsForTasks batch-loads tags for multiple tasks. Returns a map of taskID -> tags.
func (d *DB) GetTagsForTasks(taskIDs []string) (map[string][]string, error) {
	result := make(map[string][]string)
	if len(taskIDs) == 0 {
		return result, nil
	}

	ph, args := inPlaceholders(taskIDs)

	rows, err := d.Query(`SELECT task_id, tag FROM task_tags WHERE task_id IN (`+ph+`) ORDER BY tag`, args...)
	if err != nil {
		return nil, fmt.Errorf("get tags for tasks: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var taskID, tag string
		if err := rows.Scan(&taskID, &tag); err != nil {
			return nil, err
		}
		result[taskID] = append(result[taskID], tag)
	}
	return result, rows.Err()
}

// ListTagsForEpic returns all distinct tags used by an epic's children.
func (d *DB) ListTagsForEpic(epicID string) ([]string, error) {
	rows, err := d.Query(`SELECT DISTINCT tt.tag FROM task_tags tt JOIN tasks t ON t.id = tt.task_id WHERE t.epic_id = ? ORDER BY tt.tag`, epicID)
	if err != nil {
		return nil, fmt.Errorf("list tags for epic: %w", err)
	}
	defer rows.Close()
	return scanStrings(rows)
}

// ListAllTags returns all distinct tags in use.
func (d *DB) ListAllTags() ([]string, error) {
	rows, err := d.Query(`SELECT DISTINCT tag FROM task_tags ORDER BY tag`)
	if err != nil {
		return nil, fmt.Errorf("list all tags: %w", err)
	}
	defer rows.Close()
	return scanStrings(rows)
}
