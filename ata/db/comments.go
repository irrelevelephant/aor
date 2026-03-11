package db

import (
	"fmt"

	"aor/ata/model"
)

// AddComment adds a comment to a task.
func (d *DB) AddComment(taskID, body, author string) (*model.Comment, error) {
	if author == "" {
		author = model.AuthorHuman
	}

	// Verify task exists.
	var exists int
	if err := d.QueryRow(`SELECT 1 FROM tasks WHERE id = ?`, taskID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	res, err := d.Exec(`INSERT INTO comments (task_id, body, author) VALUES (?, ?, ?)`, taskID, body, author)
	if err != nil {
		return nil, fmt.Errorf("insert comment: %w", err)
	}

	id, _ := res.LastInsertId()
	return d.getComment(int(id))
}

// ListComments returns all comments for a task.
func (d *DB) ListComments(taskID string) ([]model.Comment, error) {
	rows, err := d.Query(`SELECT id, task_id, body, author, created_at FROM comments WHERE task_id = ? ORDER BY created_at ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()

	var comments []model.Comment
	for rows.Next() {
		var c model.Comment
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Body, &c.Author, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan comment: %w", err)
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

func (d *DB) getComment(id int) (*model.Comment, error) {
	c := &model.Comment{}
	err := d.QueryRow(`SELECT id, task_id, body, author, created_at FROM comments WHERE id = ?`, id).
		Scan(&c.ID, &c.TaskID, &c.Body, &c.Author, &c.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get comment: %w", err)
	}
	return c, nil
}
