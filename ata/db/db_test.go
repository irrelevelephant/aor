package db

import (
	"path/filepath"
	"testing"

	"aor/ata/model"
)

func isBlocked(d *DB, taskID string) (bool, error) {
	blockers, err := d.GetBlockers(taskID, true)
	return len(blockers) > 0, err
}

func testDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestCreateAndGetTask(t *testing.T) {
	d := testDB(t)

	task, err := d.CreateTask("Test task", "body text", model.StatusBacklog, "", "/test/workspace", "")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.Title != "Test task" {
		t.Errorf("title = %q, want %q", task.Title, "Test task")
	}
	if task.Status != model.StatusBacklog {
		t.Errorf("status = %q, want %q", task.Status, model.StatusBacklog)
	}
	if len(task.ID) != 3 {
		t.Errorf("ID length = %d, want 3", len(task.ID))
	}

	// Get it back.
	got, err := d.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Title != task.Title {
		t.Errorf("GetTask title = %q, want %q", got.Title, task.Title)
	}
}

func TestListTasks(t *testing.T) {
	d := testDB(t)

	d.CreateTask("A", "", model.StatusQueue, "", "/ws1", "")
	d.CreateTask("B", "", model.StatusBacklog, "", "/ws1", "")
	d.CreateTask("C", "", model.StatusQueue, "", "/ws2", "")

	tasks, err := d.ListTasks("/ws1", "", "", "", "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("got %d tasks for /ws1, want 2", len(tasks))
	}

	tasks, err = d.ListTasks("", model.StatusQueue, "", "", "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("got %d queue tasks, want 2", len(tasks))
	}
}

func TestClaimAndUnclaim(t *testing.T) {
	d := testDB(t)

	task, _ := d.CreateTask("Claimable", "", model.StatusQueue, "", "/ws", "")

	claimed, err := d.ClaimTask(task.ID)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if claimed.Status != model.StatusInProgress {
		t.Errorf("status = %q, want in_progress", claimed.Status)
	}

	unclaimed, err := d.UnclaimTask(task.ID)
	if err != nil {
		t.Fatalf("UnclaimTask: %v", err)
	}
	if unclaimed.Status != model.StatusQueue {
		t.Errorf("status = %q, want queue", unclaimed.Status)
	}
}

func TestCloseAndReopen(t *testing.T) {
	d := testDB(t)

	task, _ := d.CreateTask("Closable", "", model.StatusQueue, "", "/ws", "")

	closed, err := d.CloseTask(task.ID, "done")
	if err != nil {
		t.Fatalf("CloseTask: %v", err)
	}
	if closed.Status != model.StatusClosed {
		t.Errorf("status = %q, want closed", closed.Status)
	}
	if closed.CloseReason != "done" {
		t.Errorf("close_reason = %q, want %q", closed.CloseReason, "done")
	}

	reopened, err := d.ReopenTask(task.ID)
	if err != nil {
		t.Fatalf("ReopenTask: %v", err)
	}
	if reopened.Status != model.StatusBacklog {
		t.Errorf("status = %q, want backlog", reopened.Status)
	}
}

func TestPromoteToEpic(t *testing.T) {
	d := testDB(t)

	task, _ := d.CreateTask("Epic candidate", "", model.StatusQueue, "", "/ws", "")

	epic, err := d.PromoteToEpic(task.ID, "# Spec\nDo things")
	if err != nil {
		t.Fatalf("PromoteToEpic: %v", err)
	}
	if !epic.IsEpic {
		t.Error("expected is_epic = true")
	}
	if epic.Spec != "# Spec\nDo things" {
		t.Errorf("spec = %q", epic.Spec)
	}
}

func TestEpicCloseEligible(t *testing.T) {
	d := testDB(t)

	// Create epic with 2 children.
	epic, _ := d.CreateTask("My Epic", "", model.StatusQueue, "", "/ws", "")
	d.PromoteToEpic(epic.ID, "")

	c1, _ := d.CreateTask("Child 1", "", model.StatusQueue, epic.ID, "/ws", "")
	c2, _ := d.CreateTask("Child 2", "", model.StatusQueue, epic.ID, "/ws", "")

	// Not eligible yet.
	eligible, _ := d.EpicCloseEligible("/ws")
	if len(eligible) != 0 {
		t.Errorf("expected 0 eligible, got %d", len(eligible))
	}

	// Close both children.
	d.CloseTask(c1.ID, "done")
	d.CloseTask(c2.ID, "done")

	eligible, _ = d.EpicCloseEligible("/ws")
	if len(eligible) != 1 {
		t.Fatalf("expected 1 eligible, got %d", len(eligible))
	}
	if eligible[0].ID != epic.ID {
		t.Errorf("expected epic %s, got %s", epic.ID, eligible[0].ID)
	}
}

func TestComments(t *testing.T) {
	d := testDB(t)

	task, _ := d.CreateTask("Commented task", "", model.StatusQueue, "", "/ws", "")

	c, err := d.AddComment(task.ID, "first comment", model.AuthorHuman)
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if c.Body != "first comment" {
		t.Errorf("body = %q", c.Body)
	}

	d.AddComment(task.ID, "second comment", model.AuthorAgent)

	comments, err := d.ListComments(task.ID)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(comments) != 2 {
		t.Errorf("got %d comments, want 2", len(comments))
	}
}

func TestReorder(t *testing.T) {
	d := testDB(t)

	t1, _ := d.CreateTask("First", "", model.StatusQueue, "", "/ws", "")
	t2, _ := d.CreateTask("Second", "", model.StatusQueue, "", "/ws", "")
	t3, _ := d.CreateTask("Third", "", model.StatusQueue, "", "/ws", "")

	// Move third to position 0 (top).
	if err := d.Reorder(t3.ID, 0, ""); err != nil {
		t.Fatalf("Reorder: %v", err)
	}

	tasks, _ := d.ReadyTasks("/ws", "", "", 0)
	if len(tasks) < 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
	if tasks[0].ID != t3.ID {
		t.Errorf("first task should be %s, got %s", t3.ID, tasks[0].ID)
	}

	_ = t1
	_ = t2
}

func TestDeps(t *testing.T) {
	d := testDB(t)

	a, _ := d.CreateTask("Task A", "", model.StatusQueue, "", "/ws", "")
	b, _ := d.CreateTask("Task B", "", model.StatusQueue, "", "/ws", "")
	c, _ := d.CreateTask("Task C", "", model.StatusQueue, "", "/ws", "")

	// B depends on A.
	if err := d.AddDep(b.ID, a.ID); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	// B should be blocked.
	blocked, _ := isBlocked(d, b.ID)
	if !blocked {
		t.Error("expected B to be blocked")
	}

	// A should not be blocked.
	blocked, _ = isBlocked(d, a.ID)
	if blocked {
		t.Error("expected A to not be blocked")
	}

	// ReadyTasks should exclude B.
	ready, _ := d.ReadyTasks("/ws", "", "", 0)
	for _, r := range ready {
		if r.ID == b.ID {
			t.Errorf("B should not be in ready tasks")
		}
	}

	// ClaimTask should reject B.
	_, err := d.ClaimTask(b.ID)
	if err == nil {
		t.Error("expected ClaimTask on blocked task to fail")
	}

	// GetBlockers returns A.
	blockers, _ := d.GetBlockers(b.ID, true)
	if len(blockers) != 1 || blockers[0].ID != a.ID {
		t.Errorf("expected blocker A, got %v", blockers)
	}

	// GetBlocking on A returns B.
	blocking, _ := d.GetBlocking(a.ID)
	if len(blocking) != 1 || blocking[0].ID != b.ID {
		t.Errorf("expected blocking B, got %v", blocking)
	}

	// Close A, B should be unblocked.
	d.CloseTask(a.ID, "done")
	blocked, _ = isBlocked(d, b.ID)
	if blocked {
		t.Error("expected B to be unblocked after closing A")
	}

	// Now B should be in ready tasks.
	ready, _ = d.ReadyTasks("/ws", "", "", 0)
	found := false
	for _, r := range ready {
		if r.ID == b.ID {
			found = true
		}
	}
	if !found {
		t.Error("expected B in ready tasks after closing A")
	}

	// Self-dep should fail.
	if err := d.AddDep(a.ID, a.ID); err == nil {
		t.Error("expected self-dep to fail")
	}

	// Cycle detection: C depends on B, B depends on A (closed). Try A depends on C.
	d.ReopenTask(a.ID)
	d.AddDep(c.ID, b.ID)
	if err := d.AddDep(a.ID, c.ID); err == nil {
		t.Error("expected cycle detection to fail: A->C->B->A")
	}

	// Remove dep should work.
	if err := d.RemoveDep(b.ID, a.ID); err != nil {
		t.Fatalf("RemoveDep: %v", err)
	}
	blockers, _ = d.GetBlockers(b.ID, false)
	if len(blockers) != 0 {
		t.Errorf("expected no blockers after remove, got %d", len(blockers))
	}

	// BlockedTaskIDs bulk check.
	d.AddDep(b.ID, a.ID)
	d.ReopenTask(a.ID)
	ids, _ := d.BlockedTaskIDs([]string{a.ID, b.ID, c.ID})
	if !ids[b.ID] {
		t.Error("expected B to be in blocked IDs")
	}
	if !ids[c.ID] {
		t.Error("expected C to be in blocked IDs")
	}
	if ids[a.ID] {
		t.Error("expected A to not be in blocked IDs")
	}
}

func TestGetTaskWithComments(t *testing.T) {
	d := testDB(t)

	task, _ := d.CreateTask("With comments", "", model.StatusQueue, "", "/ws", "")
	d.AddComment(task.ID, "comment 1", model.AuthorHuman)

	twc, err := d.GetTaskWithComments(task.ID)
	if err != nil {
		t.Fatalf("GetTaskWithComments: %v", err)
	}
	if twc.Title != "With comments" {
		t.Errorf("title = %q", twc.Title)
	}
	if len(twc.Comments) != 1 {
		t.Errorf("comments = %d, want 1", len(twc.Comments))
	}
}
