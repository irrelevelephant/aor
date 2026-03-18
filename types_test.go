package main

import (
	"encoding/json"
	"testing"
)

func TestFiledTaskUnmarshalJSON_Object(t *testing.T) {
	input := `{"id": "abc", "title": "Fix the widget"}`
	var ft FiledTask
	if err := json.Unmarshal([]byte(input), &ft); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft.ID != "abc" {
		t.Errorf("ID = %q, want %q", ft.ID, "abc")
	}
	if ft.Title != "Fix the widget" {
		t.Errorf("Title = %q, want %q", ft.Title, "Fix the widget")
	}
}

func TestFiledTaskUnmarshalJSON_BareString(t *testing.T) {
	input := `"lpy"`
	var ft FiledTask
	if err := json.Unmarshal([]byte(input), &ft); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft.ID != "lpy" {
		t.Errorf("ID = %q, want %q", ft.ID, "lpy")
	}
	if ft.Title != "" {
		t.Errorf("Title = %q, want empty", ft.Title)
	}
}

func TestFiledTaskUnmarshalJSON_InSlice(t *testing.T) {
	// Mixed array: bare strings and objects together.
	input := `["lpy", {"id": "0tb", "title": "Add tests"}, "xyz"]`
	var tasks []FiledTask
	if err := json.Unmarshal([]byte(input), &tasks); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("got %d tasks, want 3", len(tasks))
	}
	if tasks[0].ID != "lpy" || tasks[0].Title != "" {
		t.Errorf("tasks[0] = %+v, want {ID:lpy Title:}", tasks[0])
	}
	if tasks[1].ID != "0tb" || tasks[1].Title != "Add tests" {
		t.Errorf("tasks[1] = %+v, want {ID:0tb Title:Add tests}", tasks[1])
	}
	if tasks[2].ID != "xyz" || tasks[2].Title != "" {
		t.Errorf("tasks[2] = %+v, want {ID:xyz Title:}", tasks[2])
	}
}

func TestFiledTaskUnmarshalJSON_Invalid(t *testing.T) {
	input := `123`
	var ft FiledTask
	if err := json.Unmarshal([]byte(input), &ft); err == nil {
		t.Error("expected error for numeric input, got nil")
	}
}

func TestParseSentinelJSON_BareStringTasksFiled(t *testing.T) {
	// Simulates the exact failing output from the bug report.
	raw := `Some verification output here...
EPIC_VERIFY_STATUS:{"passed": false, "tasks_filed": ["lpy", "0tb"], "summary": "Two criteria not met", "error": null}
Done.`
	status := parseSentinelJSON[EpicVerifyStatus](raw, "EPIC_VERIFY_STATUS:")
	if status == nil {
		t.Fatal("parseSentinelJSON returned nil, want non-nil")
	}
	if status.Passed {
		t.Error("Passed = true, want false")
	}
	if len(status.TasksFiled) != 2 {
		t.Fatalf("TasksFiled len = %d, want 2", len(status.TasksFiled))
	}
	if status.TasksFiled[0].ID != "lpy" {
		t.Errorf("TasksFiled[0].ID = %q, want %q", status.TasksFiled[0].ID, "lpy")
	}
	if status.TasksFiled[1].ID != "0tb" {
		t.Errorf("TasksFiled[1].ID = %q, want %q", status.TasksFiled[1].ID, "0tb")
	}
}

func TestParseSentinelJSON_ObjectTasksFiled(t *testing.T) {
	raw := `EPIC_VERIFY_STATUS:{"passed": false, "tasks_filed": [{"id": "abc", "title": "Fix X"}], "summary": "One gap", "error": null}`
	status := parseSentinelJSON[EpicVerifyStatus](raw, "EPIC_VERIFY_STATUS:")
	if status == nil {
		t.Fatal("parseSentinelJSON returned nil")
	}
	if len(status.TasksFiled) != 1 {
		t.Fatalf("TasksFiled len = %d, want 1", len(status.TasksFiled))
	}
	if status.TasksFiled[0].ID != "abc" || status.TasksFiled[0].Title != "Fix X" {
		t.Errorf("TasksFiled[0] = %+v, want {ID:abc Title:Fix X}", status.TasksFiled[0])
	}
}
