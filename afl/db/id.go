package db

import (
	"database/sql"
	"fmt"
	"strings"

	"aor/afl/model"
)

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

// prefixCols adds a table alias prefix to each column in a comma-separated list.
func prefixCols(alias, cols string) string {
	parts := strings.Split(cols, ", ")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}
