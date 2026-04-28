package db

import (
	"path/filepath"
	"testing"
)

// TestMigrateV9MergesSpecIntoBody simulates a pre-v9 DB row with both body and
// spec, then ensures the v9 migration merges spec into body and drops the column.
func TestMigrateV9MergesSpecIntoBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	// Re-add spec column to simulate a pre-v9 schema, then write rows.
	if _, err := d.Exec(`ALTER TABLE tasks ADD COLUMN spec TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatalf("re-add spec: %v", err)
	}

	cases := []struct {
		id, body, spec, want string
	}{
		{"a", "body only", "", "body only"},
		{"b", "", "spec only", "spec only"},
		{"c", "body", "spec", "body\n\nspec"},
		{"d", "", "", ""},
	}
	for _, c := range cases {
		if _, err := d.Exec(`INSERT INTO tasks (id, title, body, spec) VALUES (?, ?, ?, ?)`,
			c.id, "T", c.body, c.spec); err != nil {
			t.Fatalf("insert %s: %v", c.id, err)
		}
	}

	// Run the v9 migration manually.
	if err := d.migrateV9(); err != nil {
		t.Fatalf("migrateV9: %v", err)
	}

	for _, c := range cases {
		task, err := d.GetTask(c.id)
		if err != nil {
			t.Fatalf("GetTask %s: %v", c.id, err)
		}
		if task.Body != c.want {
			t.Errorf("%s body = %q, want %q", c.id, task.Body, c.want)
		}
	}

	// Spec column should be gone.
	var n int
	row := d.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('tasks') WHERE name = 'spec'`)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count spec col: %v", err)
	}
	if n != 0 {
		t.Errorf("spec column still present after v9")
	}
}
