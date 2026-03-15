package db

import (
	"fmt"

	"aor/ata/model"
)

// CreateAttachment inserts a new attachment record.
func (d *DB) CreateAttachment(taskID, filename, mimeType string, sizeBytes int64) (*model.Attachment, error) {
	// Verify task exists.
	var exists int
	if err := d.QueryRow(`SELECT 1 FROM tasks WHERE id = ?`, taskID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	id, err := d.generateUniqueIDForTable(3, "attachments")
	if err != nil {
		return nil, err
	}

	storedName := id + "_" + filename

	_, err = d.Exec(`INSERT INTO attachments (id, task_id, filename, stored_name, mime_type, size_bytes) VALUES (?, ?, ?, ?, ?, ?)`,
		id, taskID, filename, storedName, mimeType, sizeBytes)
	if err != nil {
		return nil, fmt.Errorf("insert attachment: %w", err)
	}

	return d.GetAttachment(id)
}

// ListAttachments returns all attachments for a task.
func (d *DB) ListAttachments(taskID string) ([]model.Attachment, error) {
	rows, err := d.Query(`SELECT id, task_id, filename, stored_name, mime_type, size_bytes, created_at FROM attachments WHERE task_id = ? ORDER BY created_at ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list attachments: %w", err)
	}
	defer rows.Close()

	var attachments []model.Attachment
	for rows.Next() {
		var a model.Attachment
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Filename, &a.StoredName, &a.MimeType, &a.SizeBytes, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan attachment: %w", err)
		}
		attachments = append(attachments, a)
	}
	return attachments, rows.Err()
}

// GetAttachment fetches a single attachment by ID.
func (d *DB) GetAttachment(id string) (*model.Attachment, error) {
	a := &model.Attachment{}
	err := d.QueryRow(`SELECT id, task_id, filename, stored_name, mime_type, size_bytes, created_at FROM attachments WHERE id = ?`, id).
		Scan(&a.ID, &a.TaskID, &a.Filename, &a.StoredName, &a.MimeType, &a.SizeBytes, &a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get attachment: %w", err)
	}
	return a, nil
}

// DeleteAttachment removes an attachment record by ID.
func (d *DB) DeleteAttachment(id string) error {
	res, err := d.Exec(`DELETE FROM attachments WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete attachment: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("attachment %s not found", id)
	}
	return nil
}
