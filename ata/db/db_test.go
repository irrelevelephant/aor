package db

import (
	"os"
	"path/filepath"
	"strings"
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

	claimed, err := d.ClaimTask(task.ID, os.Getpid())
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

func TestCloseEpicWithOpenSubtasks(t *testing.T) {
	d := testDB(t)

	epic, _ := d.CreateTask("My Epic", "", model.StatusQueue, "", "/ws", "")
	d.PromoteToEpic(epic.ID, "")

	c1, _ := d.CreateTask("Child 1", "", model.StatusQueue, epic.ID, "/ws", "")
	d.CreateTask("Child 2", "", model.StatusQueue, epic.ID, "/ws", "")

	// Should not be able to close epic with open subtasks.
	_, err := d.CloseTask(epic.ID, "done")
	if err == nil {
		t.Fatal("expected error closing epic with open subtasks")
	}
	if !strings.Contains(err.Error(), "still open") {
		t.Errorf("unexpected error: %v", err)
	}

	// Close one child — still should fail.
	d.CloseTask(c1.ID, "done")
	_, err = d.CloseTask(epic.ID, "done")
	if err == nil {
		t.Fatal("expected error closing epic with 1 open subtask")
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
	_, err := d.ClaimTask(b.ID, os.Getpid())
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

func TestTags(t *testing.T) {
	d := testDB(t)

	task1, _ := d.CreateTask("Tagged 1", "", model.StatusQueue, "", "/ws", "")
	task2, _ := d.CreateTask("Tagged 2", "", model.StatusQueue, "", "/ws", "")

	t.Run("AddAndGetTags", func(t *testing.T) {
		d.AddTag(task1.ID, "backend")
		d.AddTag(task1.ID, "api")
		d.AddTag(task1.ID, "urgent")

		tags, err := d.GetTags(task1.ID)
		if err != nil {
			t.Fatalf("GetTags: %v", err)
		}
		if len(tags) != 3 {
			t.Fatalf("got %d tags, want 3", len(tags))
		}
		// Should be sorted alphabetically.
		if tags[0] != "api" || tags[1] != "backend" || tags[2] != "urgent" {
			t.Errorf("tags = %v, want [api backend urgent]", tags)
		}
	})

	t.Run("CaseNormalization", func(t *testing.T) {
		d.AddTag(task2.ID, "UPPER")
		tags, _ := d.GetTags(task2.ID)
		if len(tags) != 1 || tags[0] != "upper" {
			t.Errorf("expected lowercase tag, got %v", tags)
		}
	})

	t.Run("EmptyTagRejection", func(t *testing.T) {
		err := d.AddTag(task1.ID, "")
		if err == nil {
			t.Error("expected error for empty tag")
		}
		err = d.AddTag(task1.ID, "   ")
		if err == nil {
			t.Error("expected error for whitespace-only tag")
		}
	})

	t.Run("RemoveTag", func(t *testing.T) {
		err := d.RemoveTag(task1.ID, "urgent")
		if err != nil {
			t.Fatalf("RemoveTag: %v", err)
		}
		tags, _ := d.GetTags(task1.ID)
		if len(tags) != 2 {
			t.Errorf("got %d tags after removal, want 2", len(tags))
		}

		// Remove non-existent tag.
		err = d.RemoveTag(task1.ID, "nonexistent")
		if err == nil {
			t.Error("expected error removing non-existent tag")
		}
	})

	t.Run("GetTagsForTasks", func(t *testing.T) {
		tagMap, err := d.GetTagsForTasks([]string{task1.ID, task2.ID})
		if err != nil {
			t.Fatalf("GetTagsForTasks: %v", err)
		}
		if len(tagMap[task1.ID]) != 2 {
			t.Errorf("task1 tags = %d, want 2", len(tagMap[task1.ID]))
		}
		if len(tagMap[task2.ID]) != 1 {
			t.Errorf("task2 tags = %d, want 1", len(tagMap[task2.ID]))
		}
	})

	t.Run("ListAllTags", func(t *testing.T) {
		tags, err := d.ListAllTags("")
		if err != nil {
			t.Fatalf("ListAllTags: %v", err)
		}
		if len(tags) < 3 {
			t.Errorf("expected at least 3 tags, got %d", len(tags))
		}

		// Workspace filter.
		tags, err = d.ListAllTags("/ws")
		if err != nil {
			t.Fatalf("ListAllTags with workspace: %v", err)
		}
		if len(tags) < 3 {
			t.Errorf("expected at least 3 tags for /ws, got %d", len(tags))
		}

		tags, err = d.ListAllTags("/nonexistent")
		if err != nil {
			t.Fatalf("ListAllTags with nonexistent workspace: %v", err)
		}
		if len(tags) != 0 {
			t.Errorf("expected 0 tags for nonexistent workspace, got %d", len(tags))
		}
	})

	t.Run("ListTagsForEpic", func(t *testing.T) {
		epic, _ := d.CreateTask("Tag Epic", "", model.StatusQueue, "", "/ws", "")
		d.PromoteToEpic(epic.ID, "")
		child, _ := d.CreateTask("Tag Child", "", model.StatusQueue, epic.ID, "/ws", "")
		d.AddTag(child.ID, "epic-tag")

		tags, err := d.ListTagsForEpic(epic.ID)
		if err != nil {
			t.Fatalf("ListTagsForEpic: %v", err)
		}
		if len(tags) != 1 || tags[0] != "epic-tag" {
			t.Errorf("expected [epic-tag], got %v", tags)
		}
	})
}

func TestWorkspaces(t *testing.T) {
	d := testDB(t)

	t.Run("RegisterAndCheck", func(t *testing.T) {
		err := d.RegisterWorkspace("/test/ws", "testws")
		if err != nil {
			t.Fatalf("RegisterWorkspace: %v", err)
		}

		exists, err := d.IsRegisteredWorkspace("/test/ws")
		if err != nil {
			t.Fatalf("IsRegisteredWorkspace: %v", err)
		}
		if !exists {
			t.Error("expected workspace to be registered")
		}

		exists, err = d.IsRegisteredWorkspace("/nonexistent")
		if err != nil {
			t.Fatalf("IsRegisteredWorkspace: %v", err)
		}
		if exists {
			t.Error("expected workspace to not be registered")
		}
	})

	t.Run("ResolveByName", func(t *testing.T) {
		path, err := d.ResolveWorkspace("testws")
		if err != nil {
			t.Fatalf("ResolveWorkspace by name: %v", err)
		}
		if path != "/test/ws" {
			t.Errorf("path = %q, want /test/ws", path)
		}
	})

	t.Run("ResolveByPath", func(t *testing.T) {
		path, err := d.ResolveWorkspace("/test/ws")
		if err != nil {
			t.Fatalf("ResolveWorkspace by path: %v", err)
		}
		if path != "/test/ws" {
			t.Errorf("path = %q, want /test/ws", path)
		}
	})

	t.Run("ResolveNotFound", func(t *testing.T) {
		_, err := d.ResolveWorkspace("nonexistent")
		if err == nil {
			t.Error("expected error for nonexistent workspace")
		}
	})

	t.Run("Unregister", func(t *testing.T) {
		err := d.UnregisterWorkspace("/test/ws")
		if err != nil {
			t.Fatalf("UnregisterWorkspace: %v", err)
		}
		exists, _ := d.IsRegisteredWorkspace("/test/ws")
		if exists {
			t.Error("expected workspace to be unregistered")
		}
	})
}

func TestCreateTaskSortOrder(t *testing.T) {
	d := testDB(t)

	t1, err := d.CreateTask("First", "", model.StatusQueue, "", "/ws", "")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	t2, err := d.CreateTask("Second", "", model.StatusQueue, "", "/ws", "")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	t3, err := d.CreateTask("Third", "", model.StatusQueue, "", "/ws", "")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if t1.SortOrder != 1 {
		t.Errorf("t1 sort_order = %d, want 1", t1.SortOrder)
	}
	if t2.SortOrder != 2 {
		t.Errorf("t2 sort_order = %d, want 2", t2.SortOrder)
	}
	if t3.SortOrder != 3 {
		t.Errorf("t3 sort_order = %d, want 3", t3.SortOrder)
	}
}

func TestClaimBlockedTask(t *testing.T) {
	d := testDB(t)

	a, _ := d.CreateTask("Blocker", "", model.StatusQueue, "", "/ws", "")
	b, _ := d.CreateTask("Blocked", "", model.StatusQueue, "", "/ws", "")

	if err := d.AddDep(b.ID, a.ID); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	_, err := d.ClaimTask(b.ID, os.Getpid())
	if err == nil {
		t.Fatal("expected error claiming blocked task")
	}
	if !strings.Contains(err.Error(), "blocked by") {
		t.Errorf("unexpected error: %v", err)
	}

	// Close blocker, claim should succeed.
	d.CloseTask(a.ID, "done")
	_, err = d.ClaimTask(b.ID, os.Getpid())
	if err != nil {
		t.Fatalf("ClaimTask after unblocking: %v", err)
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
