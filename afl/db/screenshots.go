package db

import (
	"database/sql"
	"fmt"

	"aor/afl/model"
)

const screenshotCols = `id, step_id, platform, filename, stored_name, mime_type, size_bytes, capture_source, captured_at, created_at`

// UpsertScreenshot inserts or replaces a screenshot for a (step_id, platform) pair.
func (d *DB) UpsertScreenshot(stepID, platform, filename, storedName, mimeType string, sizeBytes int64, captureSource, capturedAt string) (*model.Screenshot, error) {
	id, err := d.generateUniqueIDForTable(3, "screenshots")
	if err != nil {
		return nil, err
	}

	_, err = d.Exec(`INSERT INTO screenshots (id, step_id, platform, filename, stored_name, mime_type, size_bytes, capture_source, captured_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(step_id, platform) DO UPDATE SET
			filename = excluded.filename,
			stored_name = excluded.stored_name,
			mime_type = excluded.mime_type,
			size_bytes = excluded.size_bytes,
			capture_source = excluded.capture_source,
			captured_at = excluded.captured_at,
			created_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')`,
		id, stepID, platform, filename, storedName, mimeType, sizeBytes, captureSource, capturedAt)
	if err != nil {
		return nil, fmt.Errorf("upsert screenshot: %w", err)
	}

	return d.GetScreenshotForStep(stepID, platform)
}

// GetScreenshot returns a single screenshot by ID.
func (d *DB) GetScreenshot(id string) (*model.Screenshot, error) {
	var s model.Screenshot
	err := d.QueryRow(`SELECT `+screenshotCols+` FROM screenshots WHERE id = ?`, id).
		Scan(&s.ID, &s.StepID, &s.Platform, &s.Filename, &s.StoredName, &s.MimeType, &s.SizeBytes, &s.CaptureSource, &s.CapturedAt, &s.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("screenshot not found")
		}
		return nil, fmt.Errorf("scan screenshot: %w", err)
	}
	return &s, nil
}

// GetScreenshotForStep returns the screenshot for a step and platform.
func (d *DB) GetScreenshotForStep(stepID, platform string) (*model.Screenshot, error) {
	var s model.Screenshot
	err := d.QueryRow(`SELECT `+screenshotCols+` FROM screenshots WHERE step_id = ? AND platform = ?`, stepID, platform).
		Scan(&s.ID, &s.StepID, &s.Platform, &s.Filename, &s.StoredName, &s.MimeType, &s.SizeBytes, &s.CaptureSource, &s.CapturedAt, &s.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan screenshot: %w", err)
	}
	return &s, nil
}

// ListScreenshotsForPath returns all screenshots for all steps in a path.
func (d *DB) ListScreenshotsForPath(pathID string) ([]model.Screenshot, error) {
	rows, err := d.Query(`SELECT `+prefixCols("sc", screenshotCols)+` FROM screenshots sc
		JOIN steps st ON st.id = sc.step_id
		WHERE st.path_id = ?
		ORDER BY st.sort_order ASC, sc.platform ASC`, pathID)
	if err != nil {
		return nil, fmt.Errorf("list screenshots for path: %w", err)
	}
	defer rows.Close()

	return scanScreenshotRows(rows)
}

// ListScreenshotsForFlow returns all screenshots for all paths/steps in a flow.
func (d *DB) ListScreenshotsForFlow(flowID string) ([]model.Screenshot, error) {
	rows, err := d.Query(`SELECT `+prefixCols("sc", screenshotCols)+` FROM screenshots sc
		JOIN steps st ON st.id = sc.step_id
		JOIN paths p ON p.id = st.path_id
		WHERE p.flow_id = ?
		ORDER BY p.sort_order ASC, st.sort_order ASC, sc.platform ASC`, flowID)
	if err != nil {
		return nil, fmt.Errorf("list screenshots for flow: %w", err)
	}
	defer rows.Close()

	return scanScreenshotRows(rows)
}

// DeleteScreenshot deletes a screenshot by ID and returns its stored_name so the caller can delete the file.
func (d *DB) DeleteScreenshot(id string) (string, error) {
	var storedName string
	err := d.QueryRow(`SELECT stored_name FROM screenshots WHERE id = ?`, id).Scan(&storedName)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("screenshot %s not found", id)
		}
		return "", fmt.Errorf("get screenshot: %w", err)
	}

	_, err = d.Exec(`DELETE FROM screenshots WHERE id = ?`, id)
	if err != nil {
		return "", fmt.Errorf("delete screenshot: %w", err)
	}
	return storedName, nil
}

// ScreenshotWithPath holds a screenshot and its flow ID for file path resolution.
type ScreenshotWithPath struct {
	model.Screenshot
	FlowID string
}

// GetScreenshotWithPath returns a screenshot with its flow ID in a single query.
func (d *DB) GetScreenshotWithPath(id string) (*ScreenshotWithPath, error) {
	var s ScreenshotWithPath
	err := d.QueryRow(`
		SELECT sc.id, sc.step_id, sc.platform, sc.filename, sc.stored_name,
		       sc.mime_type, sc.size_bytes, sc.capture_source, sc.captured_at, sc.created_at,
		       p.flow_id
		FROM screenshots sc
		JOIN steps st ON st.id = sc.step_id
		JOIN paths p ON p.id = st.path_id
		WHERE sc.id = ?`, id).
		Scan(&s.ID, &s.StepID, &s.Platform, &s.Filename, &s.StoredName,
			&s.MimeType, &s.SizeBytes, &s.CaptureSource, &s.CapturedAt, &s.CreatedAt,
			&s.FlowID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get screenshot with path: %w", err)
	}
	return &s, nil
}

func scanScreenshotRows(rows *sql.Rows) ([]model.Screenshot, error) {
	var screenshots []model.Screenshot
	for rows.Next() {
		var s model.Screenshot
		if err := rows.Scan(&s.ID, &s.StepID, &s.Platform, &s.Filename, &s.StoredName, &s.MimeType, &s.SizeBytes, &s.CaptureSource, &s.CapturedAt, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan screenshot row: %w", err)
		}
		screenshots = append(screenshots, s)
	}
	return screenshots, rows.Err()
}
