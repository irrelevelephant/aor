package db

import (
	"testing"
	"time"

	"aor/ata/model"
)

func TestListClosedTasks_All(t *testing.T) {
	d := testDB(t)

	t1, _ := d.CreateTask("task 1", "", model.StatusBacklog, "", "")
	t2, _ := d.CreateTask("task 2", "", model.StatusBacklog, "", "")
	d.CloseTask(t1.ID, "done")
	d.CloseTask(t2.ID, "done")

	tasks, err := d.ListClosedTasks(0)
	if err != nil {
		t.Fatalf("ListClosedTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}
}

func TestListClosedTasks_AgeFilter(t *testing.T) {
	d := testDB(t)

	task, _ := d.CreateTask("old task", "", model.StatusBacklog, "", "")
	d.CloseTask(task.ID, "done")

	tasks, err := d.ListClosedTasks(999 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("ListClosedTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("got %d tasks, want 0 (task is too recent)", len(tasks))
	}

	tasks, err = d.ListClosedTasks(0)
	if err != nil {
		t.Fatalf("ListClosedTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("got %d tasks, want 1", len(tasks))
	}
}

func TestListClosedTasks_OpenTasksExcluded(t *testing.T) {
	d := testDB(t)

	d.CreateTask("open task", "", model.StatusBacklog, "", "")
	d.CreateTask("queue task", "", model.StatusQueue, "", "")

	tasks, err := d.ListClosedTasks(0)
	if err != nil {
		t.Fatalf("ListClosedTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("got %d tasks, want 0 (no closed tasks)", len(tasks))
	}
}

func TestListClosedTasks_EpicSubtasksIncluded(t *testing.T) {
	d := testDB(t)

	epic, _ := d.CreateTask("my epic", "", model.StatusBacklog, "", "")
	d.PromoteToEpic(epic.ID, "")
	sub, _ := d.CreateTask("subtask", "", model.StatusBacklog, epic.ID, "")

	d.CloseTask(sub.ID, "done")
	d.CloseTask(epic.ID, "done")

	tasks, err := d.ListClosedTasks(0)
	if err != nil {
		t.Fatalf("ListClosedTasks: %v", err)
	}

	ids := make(map[string]bool)
	for _, t := range tasks {
		ids[t.ID] = true
	}
	if !ids[epic.ID] {
		t.Errorf("epic %s not in results", epic.ID)
	}
	if !ids[sub.ID] {
		t.Errorf("subtask %s not in results", sub.ID)
	}
}

func TestGCClosedTasks_DeleteCascades(t *testing.T) {
	d := testDB(t)

	task, _ := d.CreateTask("to delete", "", model.StatusBacklog, "", "")
	d.AddComment(task.ID, "a comment", model.AuthorHuman)
	d.AddTag(task.ID, "important")
	d.CreateAttachment(task.ID, "file.txt", "text/plain", 100)

	d.CloseTask(task.ID, "done")

	deleted, err := d.GCClosedTasks([]string{task.ID})
	if err != nil {
		t.Fatalf("GCClosedTasks: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	if _, err := d.GetTask(task.ID); err == nil {
		t.Error("task still exists after GC")
	}

	comments, err := d.ListComments(task.ID)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("comments remain: %d", len(comments))
	}

	tags, err := d.GetTags(task.ID)
	if err != nil {
		t.Fatalf("GetTags: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("tags remain: %d", len(tags))
	}

	atts, err := d.ListAttachments(task.ID)
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if len(atts) != 0 {
		t.Errorf("attachments remain: %d", len(atts))
	}
}

func TestGCClosedTasks_DepsCleanedUp(t *testing.T) {
	d := testDB(t)

	a, _ := d.CreateTask("task a", "", model.StatusBacklog, "", "")
	b, _ := d.CreateTask("task b", "", model.StatusBacklog, "", "")
	d.AddDep(b.ID, a.ID)

	d.CloseTask(a.ID, "done")

	deleted, err := d.GCClosedTasks([]string{a.ID})
	if err != nil {
		t.Fatalf("GCClosedTasks: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	blockers, err := d.GetBlockers(b.ID, true)
	if err != nil {
		t.Fatalf("GetBlockers: %v", err)
	}
	if len(blockers) != 0 {
		t.Errorf("b still has blockers: %v", blockers)
	}
}

func TestGCClosedTasks_EpicRefCleared(t *testing.T) {
	d := testDB(t)

	epic, _ := d.CreateTask("epic", "", model.StatusBacklog, "", "")
	d.PromoteToEpic(epic.ID, "")
	sub, _ := d.CreateTask("open sub", "", model.StatusBacklog, epic.ID, "")

	d.CloseTask(epic.ID, "done")

	deleted, err := d.GCClosedTasks([]string{epic.ID})
	if err != nil {
		t.Fatalf("GCClosedTasks: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	got, err := d.GetTask(sub.ID)
	if err != nil {
		t.Fatalf("subtask gone: %v", err)
	}
	if got.EpicID != "" {
		t.Errorf("subtask epic_id = %q, want empty", got.EpicID)
	}
}

func TestParseDayDuration(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
		err   bool
	}{
		{"30d", 30 * 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"0d", 0, false},
		{"1d", 24 * time.Hour, false},
		{"", 0, true},
		{"30", 0, true},
		{"30h", 0, true},
		{"-1d", 0, true},
		{"abcd", 0, true},
	}
	for _, tt := range tests {
		got, err := ParseDayDuration(tt.input)
		if tt.err {
			if err == nil {
				t.Errorf("ParseDayDuration(%q): want error, got %v", tt.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseDayDuration(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseDayDuration(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestGetAttachmentSummaries(t *testing.T) {
	d := testDB(t)

	task, _ := d.CreateTask("with attachments", "", model.StatusBacklog, "", "")
	d.CreateAttachment(task.ID, "a.txt", "text/plain", 100)
	d.CreateAttachment(task.ID, "b.txt", "text/plain", 200)

	summaries, err := d.GetAttachmentSummaries([]string{task.ID})
	if err != nil {
		t.Fatalf("GetAttachmentSummaries: %v", err)
	}
	s, ok := summaries[task.ID]
	if !ok {
		t.Fatal("no summary for task")
	}
	if s.Count != 2 {
		t.Errorf("count = %d, want 2", s.Count)
	}
	if s.TotalSize != 300 {
		t.Errorf("total size = %d, want 300", s.TotalSize)
	}
}
