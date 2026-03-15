package db

import (
	"fmt"
	"time"

	"aor/ata/model"
)

// ExportWorkspace exports all data for a workspace: metadata, tasks, comments, deps, tags, and attachments.
func (d *DB) ExportWorkspace(path string) (*model.SnapshotMeta, []model.Task, []model.Comment, []model.TaskDep, []model.TaskTag, []model.Attachment, error) {
	ws, err := d.GetWorkspace(path)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("get workspace: %w", err)
	}

	var sourceName string
	if ws != nil {
		sourceName = ws.Name
	}

	meta := &model.SnapshotMeta{
		SchemaVersion: SchemaVersion(),
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		SourcePath:    path,
		SourceName:    sourceName,
	}

	tasks, err := d.ListTasks(path, "", "", "", "")
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("list tasks: %w", err)
	}

	if len(tasks) == 0 {
		return meta, nil, nil, nil, nil, nil, nil
	}

	taskIDs := make([]string, len(tasks))
	for i, t := range tasks {
		taskIDs[i] = t.ID
	}

	// Batch-query comments.
	ph, phArgs := inPlaceholders(taskIDs)
	commentRows, err := d.Query(`SELECT id, task_id, body, author, created_at FROM comments WHERE task_id IN (`+ph+`) ORDER BY created_at ASC`, phArgs...)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("query comments: %w", err)
	}
	defer commentRows.Close()

	var comments []model.Comment
	for commentRows.Next() {
		var c model.Comment
		if err := commentRows.Scan(&c.ID, &c.TaskID, &c.Body, &c.Author, &c.CreatedAt); err != nil {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("scan comment: %w", err)
		}
		comments = append(comments, c)
	}
	if err := commentRows.Err(); err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("comments rows: %w", err)
	}

	// Batch-query deps — both sides must be in the workspace task set.
	depRows, err := d.Query(`SELECT task_id, depends_on, created_at FROM task_deps WHERE task_id IN (`+ph+`) AND depends_on IN (`+ph+`)`, append(phArgs, phArgs...)...)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("query deps: %w", err)
	}
	defer depRows.Close()

	var deps []model.TaskDep
	for depRows.Next() {
		var dep model.TaskDep
		if err := depRows.Scan(&dep.TaskID, &dep.DependsOn, &dep.CreatedAt); err != nil {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("scan dep: %w", err)
		}
		deps = append(deps, dep)
	}
	if err := depRows.Err(); err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("deps rows: %w", err)
	}

	// Batch-query tags.
	tagRows, err := d.Query(`SELECT task_id, tag, created_at FROM task_tags WHERE task_id IN (`+ph+`)`, phArgs...)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("query tags: %w", err)
	}
	defer tagRows.Close()

	var tags []model.TaskTag
	for tagRows.Next() {
		var tag model.TaskTag
		if err := tagRows.Scan(&tag.TaskID, &tag.Tag, &tag.CreatedAt); err != nil {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("scan tag: %w", err)
		}
		tags = append(tags, tag)
	}
	if err := tagRows.Err(); err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("tags rows: %w", err)
	}

	// Batch-query attachments.
	attRows, err := d.Query(`SELECT id, task_id, filename, stored_name, mime_type, size_bytes, created_at FROM attachments WHERE task_id IN (`+ph+`) ORDER BY created_at ASC`, phArgs...)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("query attachments: %w", err)
	}
	defer attRows.Close()

	var attachments []model.Attachment
	for attRows.Next() {
		var a model.Attachment
		if err := attRows.Scan(&a.ID, &a.TaskID, &a.Filename, &a.StoredName, &a.MimeType, &a.SizeBytes, &a.CreatedAt); err != nil {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("scan attachment: %w", err)
		}
		attachments = append(attachments, a)
	}
	if err := attRows.Err(); err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("attachments rows: %w", err)
	}

	return meta, tasks, comments, deps, tags, attachments, nil
}

// ImportWorkspace replaces a workspace with imported data.
// The target workspace is fully wiped before import.
func (d *DB) ImportWorkspace(targetPath, targetName string, sourcePath string, tasks []model.Task, comments []model.Comment, deps []model.TaskDep, tags []model.TaskTag, attachments []model.Attachment) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Wipe existing workspace data (cascades to comments, deps, tags, attachments via FK).
	if _, err := tx.Exec(`DELETE FROM tasks WHERE workspace = ?`, targetPath); err != nil {
		return fmt.Errorf("delete tasks: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM workspaces WHERE path = ?`, targetPath); err != nil {
		return fmt.Errorf("delete workspace: %w", err)
	}

	// Insert workspace.
	if _, err := tx.Exec(`INSERT INTO workspaces (path, name) VALUES (?, ?)`, targetPath, targetName); err != nil {
		return fmt.Errorf("insert workspace: %w", err)
	}

	// Insert tasks with remapped workspace, cleared claim data.
	for _, t := range tasks {
		createdIn := t.CreatedIn
		if createdIn == sourcePath {
			createdIn = targetPath
		}

		_, err := tx.Exec(`INSERT INTO tasks (`+taskCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ID, t.Title, t.Body, t.Status, t.SortOrder,
			nullStr(t.EpicID), targetPath, "", createdIn,
			t.IsEpic, t.Spec,
			nil, nil, nullStr(t.ClosedAt), t.CloseReason,
			t.CreatedAt, t.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert task %s: %w", t.ID, err)
		}
	}

	// Insert comments (without original ID — let autoincrement assign new ones).
	for _, c := range comments {
		_, err := tx.Exec(`INSERT INTO comments (task_id, body, author, created_at) VALUES (?, ?, ?, ?)`,
			c.TaskID, c.Body, c.Author, c.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert comment for task %s: %w", c.TaskID, err)
		}
	}

	// Insert deps.
	for _, dep := range deps {
		_, err := tx.Exec(`INSERT INTO task_deps (task_id, depends_on, created_at) VALUES (?, ?, ?)`,
			dep.TaskID, dep.DependsOn, dep.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert dep %s->%s: %w", dep.TaskID, dep.DependsOn, err)
		}
	}

	// Insert tags.
	for _, tag := range tags {
		_, err := tx.Exec(`INSERT INTO task_tags (task_id, tag, created_at) VALUES (?, ?, ?)`,
			tag.TaskID, tag.Tag, tag.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert tag %s/%s: %w", tag.TaskID, tag.Tag, err)
		}
	}

	// Insert attachments.
	for _, a := range attachments {
		_, err := tx.Exec(`INSERT INTO attachments (id, task_id, filename, stored_name, mime_type, size_bytes, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			a.ID, a.TaskID, a.Filename, a.StoredName, a.MimeType, a.SizeBytes, a.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert attachment %s: %w", a.ID, err)
		}
	}

	return tx.Commit()
}
