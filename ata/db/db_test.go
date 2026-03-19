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

// makeNestedEpic creates a root epic → sub-epic → leaf task hierarchy for tests.
func makeNestedEpic(t *testing.T, d *DB) (root, sub, leaf *model.Task) {
	t.Helper()
	r, _ := d.CreateTask("Root", "", model.StatusQueue, "", "/ws", "")
	d.PromoteToEpic(r.ID, "")
	s, _ := d.CreateTask("Sub", "", model.StatusQueue, r.ID, "/ws", "")
	d.PromoteToEpic(s.ID, "")
	l, _ := d.CreateTask("Leaf", "", model.StatusQueue, s.ID, "/ws", "")
	return r, s, l
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
	if reopened.Status != model.StatusQueue {
		t.Errorf("status = %q, want queue", reopened.Status)
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

func TestReadyTasksExcludesEpics(t *testing.T) {
	d := testDB(t)

	// Create a regular task and an epic, both in queue.
	task, _ := d.CreateTask("Regular Task", "", model.StatusQueue, "", "/ws", "")
	epic, _ := d.CreateTask("My Epic", "", model.StatusQueue, "", "/ws", "")
	d.PromoteToEpic(epic.ID, "# Spec")

	ready, err := d.ReadyTasks("/ws", "", "", 0)
	if err != nil {
		t.Fatalf("ReadyTasks: %v", err)
	}

	// Should only contain the regular task, not the epic.
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready task, got %d", len(ready))
	}
	if ready[0].ID != task.ID {
		t.Errorf("expected task %s, got %s", task.ID, ready[0].ID)
	}
}

func TestReadyTasksNestedEpics(t *testing.T) {
	d := testDB(t)

	// Create a root epic with a nested sub-epic.
	//   root (epic)
	//     ├── sub (epic)
	//     │   ├── deep1 (task, queue)
	//     │   └── deep2 (task, queue)
	//     └── shallow (task, queue)
	root, _ := d.CreateTask("Root Epic", "", model.StatusQueue, "", "/ws", "")
	d.PromoteToEpic(root.ID, "# Root Spec")

	sub, _ := d.CreateTask("Sub Epic", "", model.StatusQueue, root.ID, "/ws", "")
	d.PromoteToEpic(sub.ID, "# Sub Spec")

	deep1, _ := d.CreateTask("Deep Task 1", "", model.StatusQueue, sub.ID, "/ws", "")
	deep2, _ := d.CreateTask("Deep Task 2", "", model.StatusQueue, sub.ID, "/ws", "")
	shallow, _ := d.CreateTask("Shallow Task", "", model.StatusQueue, root.ID, "/ws", "")

	// Filtering by root epic should return tasks at all nesting levels.
	ready, err := d.ReadyTasks("/ws", root.ID, "", 0)
	if err != nil {
		t.Fatalf("ReadyTasks: %v", err)
	}

	ids := map[string]bool{}
	for _, r := range ready {
		ids[r.ID] = true
	}

	if !ids[deep1.ID] {
		t.Errorf("expected deep1 (%s) in ready tasks", deep1.ID)
	}
	if !ids[deep2.ID] {
		t.Errorf("expected deep2 (%s) in ready tasks", deep2.ID)
	}
	if !ids[shallow.ID] {
		t.Errorf("expected shallow (%s) in ready tasks", shallow.ID)
	}
	if len(ready) != 3 {
		t.Errorf("expected 3 ready tasks, got %d", len(ready))
	}

	// Filtering by sub-epic should only return its children.
	ready, err = d.ReadyTasks("/ws", sub.ID, "", 0)
	if err != nil {
		t.Fatalf("ReadyTasks: %v", err)
	}
	if len(ready) != 2 {
		t.Errorf("expected 2 ready tasks for sub-epic, got %d", len(ready))
	}
}

func TestReadyTasksTripleNestedEpics(t *testing.T) {
	d := testDB(t)

	// Three levels of nesting: root → mid → leaf (epic) → task
	root, _ := d.CreateTask("Root", "", model.StatusQueue, "", "/ws", "")
	d.PromoteToEpic(root.ID, "")

	mid, _ := d.CreateTask("Mid", "", model.StatusQueue, root.ID, "/ws", "")
	d.PromoteToEpic(mid.ID, "")

	leaf, _ := d.CreateTask("Leaf Epic", "", model.StatusQueue, mid.ID, "/ws", "")
	d.PromoteToEpic(leaf.ID, "")

	task, _ := d.CreateTask("Deep Task", "", model.StatusQueue, leaf.ID, "/ws", "")

	ready, err := d.ReadyTasks("/ws", root.ID, "", 0)
	if err != nil {
		t.Fatalf("ReadyTasks: %v", err)
	}

	if len(ready) != 1 {
		t.Fatalf("expected 1 ready task, got %d", len(ready))
	}
	if ready[0].ID != task.ID {
		t.Errorf("expected task %s, got %s", task.ID, ready[0].ID)
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

func TestEpicCloseEligibleCascadesWithNesting(t *testing.T) {
	d := testDB(t)
	root, sub, task := makeNestedEpic(t, d)

	// Nothing eligible yet.
	eligible, _ := d.EpicCloseEligible("/ws")
	if len(eligible) != 0 {
		t.Fatalf("expected 0 eligible, got %d", len(eligible))
	}

	// Close the leaf task — sub should become eligible, but not root.
	d.CloseTask(task.ID, "done")
	eligible, _ = d.EpicCloseEligible("/ws")
	if len(eligible) != 1 {
		t.Fatalf("expected 1 eligible, got %d", len(eligible))
	}
	if eligible[0].ID != sub.ID {
		t.Errorf("expected sub %s eligible, got %s", sub.ID, eligible[0].ID)
	}

	// Close sub — now root should become eligible.
	d.CloseTask(sub.ID, "done")
	eligible, _ = d.EpicCloseEligible("/ws")
	if len(eligible) != 1 {
		t.Fatalf("expected 1 eligible, got %d", len(eligible))
	}
	if eligible[0].ID != root.ID {
		t.Errorf("expected root %s eligible, got %s", root.ID, eligible[0].ID)
	}
}

func TestCloseEpicBlockedByOpenSubEpic(t *testing.T) {
	d := testDB(t)
	root, sub, task := makeNestedEpic(t, d)

	d.CloseTask(task.ID, "done")

	// Root still has open direct child (sub), so close should fail.
	_, err := d.CloseTask(root.ID, "done")
	if err == nil {
		t.Fatal("expected error closing root with open sub-epic")
	}

	// Close sub first, then root should succeed.
	d.CloseTask(sub.ID, "done")
	_, err = d.CloseTask(root.ID, "done")
	if err != nil {
		t.Fatalf("expected root to close after sub closed: %v", err)
	}
}

func TestListTasksNestedEpic(t *testing.T) {
	d := testDB(t)

	// root (epic) → sub (epic) → deep task
	//             → shallow task
	root, _ := d.CreateTask("Root", "", model.StatusQueue, "", "/ws", "")
	d.PromoteToEpic(root.ID, "")

	sub, _ := d.CreateTask("Sub", "", model.StatusQueue, root.ID, "/ws", "")
	d.PromoteToEpic(sub.ID, "")

	deep, _ := d.CreateTask("Deep", "", model.StatusQueue, sub.ID, "/ws", "")
	shallow, _ := d.CreateTask("Shallow", "", model.StatusQueue, root.ID, "/ws", "")

	// ListTasks with root epic should return sub, deep, and shallow.
	tasks, err := d.ListTasks("/ws", "", root.ID, "", "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}

	ids := map[string]bool{}
	for _, task := range tasks {
		ids[task.ID] = true
	}

	if !ids[sub.ID] {
		t.Errorf("expected sub-epic %s in results", sub.ID)
	}
	if !ids[deep.ID] {
		t.Errorf("expected deep task %s in results", deep.ID)
	}
	if !ids[shallow.ID] {
		t.Errorf("expected shallow task %s in results", shallow.ID)
	}
	if len(tasks) != 3 {
		t.Errorf("expected 3 tasks, got %d", len(tasks))
	}

	// ListTasks with sub epic should only return deep task.
	tasks, err = d.ListTasks("/ws", "", sub.ID, "", "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != deep.ID {
		t.Errorf("expected only deep task %s, got %d tasks", deep.ID, len(tasks))
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

	t.Run("TaskCounts", func(t *testing.T) {
		d.RegisterWorkspace("/count/ws", "countws")
		d.CreateTask("Open1", "", model.StatusQueue, "", "/count/ws", "")
		d.CreateTask("Open2", "", model.StatusBacklog, "", "/count/ws", "")
		t3, _ := d.CreateTask("Closed1", "", model.StatusQueue, "", "/count/ws", "")
		d.CloseTask(t3.ID, "done")
		t4, _ := d.CreateTask("Closed2", "", model.StatusQueue, "", "/count/ws", "")
		d.CloseTask(t4.ID, "done")
		t5, _ := d.CreateTask("Closed3", "", model.StatusQueue, "", "/count/ws", "")
		d.CloseTask(t5.ID, "done")

		open, closed, err := d.WorkspaceTaskCounts("/count/ws")
		if err != nil {
			t.Fatalf("WorkspaceTaskCounts: %v", err)
		}
		if open != 2 {
			t.Errorf("open = %d, want 2", open)
		}
		if closed != 3 {
			t.Errorf("closed = %d, want 3", closed)
		}
	})

	t.Run("TaskCountsEmpty", func(t *testing.T) {
		d.RegisterWorkspace("/empty/ws", "emptyws")
		open, closed, err := d.WorkspaceTaskCounts("/empty/ws")
		if err != nil {
			t.Fatalf("WorkspaceTaskCounts: %v", err)
		}
		if open != 0 || closed != 0 {
			t.Errorf("expected 0/0, got %d/%d", open, closed)
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

func TestReorderDown(t *testing.T) {
	d := testDB(t)

	t1, _ := d.CreateTask("First", "", model.StatusQueue, "", "/ws", "")
	t2, _ := d.CreateTask("Second", "", model.StatusQueue, "", "/ws", "")
	t3, _ := d.CreateTask("Third", "", model.StatusQueue, "", "/ws", "")

	// Move first task to last position.
	if err := d.Reorder(t1.ID, 2, ""); err != nil {
		t.Fatalf("Reorder down: %v", err)
	}

	tasks, _ := d.ListTasks("/ws", model.StatusQueue, "", "", "")
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
	if tasks[0].ID != t2.ID {
		t.Errorf("position 0: want %s, got %s", t2.ID, tasks[0].ID)
	}
	if tasks[1].ID != t3.ID {
		t.Errorf("position 1: want %s, got %s", t3.ID, tasks[1].ID)
	}
	if tasks[2].ID != t1.ID {
		t.Errorf("position 2: want %s, got %s", t1.ID, tasks[2].ID)
	}
}

func TestReorderCrossStatus(t *testing.T) {
	d := testDB(t)

	t1, _ := d.CreateTask("Queue1", "", model.StatusQueue, "", "/ws", "")
	t2, _ := d.CreateTask("Queue2", "", model.StatusQueue, "", "/ws", "")
	b1, _ := d.CreateTask("Backlog1", "", model.StatusBacklog, "", "/ws", "")

	// Move t1 from queue to backlog at position 0.
	if err := d.Reorder(t1.ID, 0, model.StatusBacklog); err != nil {
		t.Fatalf("Reorder cross-status: %v", err)
	}

	queue, _ := d.ListTasks("/ws", model.StatusQueue, "", "", "")
	if len(queue) != 1 || queue[0].ID != t2.ID {
		t.Errorf("queue should have only t2, got %v", queue)
	}

	backlog, _ := d.ListTasks("/ws", model.StatusBacklog, "", "", "")
	if len(backlog) != 2 {
		t.Fatalf("expected 2 backlog tasks, got %d", len(backlog))
	}
	if backlog[0].ID != t1.ID {
		t.Errorf("backlog[0]: want %s, got %s", t1.ID, backlog[0].ID)
	}
	if backlog[1].ID != b1.ID {
		t.Errorf("backlog[1]: want %s, got %s", b1.ID, backlog[1].ID)
	}
}

func TestReorderInEpic(t *testing.T) {
	d := testDB(t)

	epic, _ := d.CreateTask("Epic", "", model.StatusQueue, "", "/ws", "")
	d.PromoteToEpic(epic.ID, "")

	c1, _ := d.CreateTask("Child1", "", model.StatusQueue, epic.ID, "/ws", "")
	c2, _ := d.CreateTask("Child2", "", model.StatusQueue, epic.ID, "/ws", "")
	c3, _ := d.CreateTask("Child3", "", model.StatusQueue, epic.ID, "/ws", "")

	// Move c1 to last position.
	if err := d.ReorderInEpic(c1.ID, 2, epic.ID); err != nil {
		t.Fatalf("ReorderInEpic: %v", err)
	}

	children, _ := d.ListTasks("", "", epic.ID, "", "")
	if len(children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(children))
	}
	if children[0].ID != c2.ID {
		t.Errorf("position 0: want %s, got %s", c2.ID, children[0].ID)
	}
	if children[1].ID != c3.ID {
		t.Errorf("position 1: want %s, got %s", c3.ID, children[1].ID)
	}
	if children[2].ID != c1.ID {
		t.Errorf("position 2: want %s, got %s", c1.ID, children[2].ID)
	}
}

func TestReorderOptBeforeAfter(t *testing.T) {
	d := testDB(t)

	t1, _ := d.CreateTask("First", "", model.StatusQueue, "", "/ws", "")
	t2, _ := d.CreateTask("Second", "", model.StatusQueue, "", "/ws", "")
	t3, _ := d.CreateTask("Third", "", model.StatusQueue, "", "/ws", "")

	// Move t3 before t1 (to the top).
	if err := d.ReorderOpt(t3.ID, "", ReorderOpts{Position: -1, Before: t1.ID}); err != nil {
		t.Fatalf("ReorderOpt before: %v", err)
	}
	tasks, _ := d.ListTasks("/ws", model.StatusQueue, "", "", "")
	if len(tasks) != 3 {
		t.Fatalf("expected 3, got %d", len(tasks))
	}
	if tasks[0].ID != t3.ID {
		t.Errorf("pos 0: want %s, got %s", t3.ID, tasks[0].ID)
	}
	if tasks[1].ID != t1.ID {
		t.Errorf("pos 1: want %s, got %s", t1.ID, tasks[1].ID)
	}
	if tasks[2].ID != t2.ID {
		t.Errorf("pos 2: want %s, got %s", t2.ID, tasks[2].ID)
	}

	// Move t1 after t2 (to the end).
	if err := d.ReorderOpt(t1.ID, "", ReorderOpts{Position: -1, After: t2.ID}); err != nil {
		t.Fatalf("ReorderOpt after: %v", err)
	}
	tasks, _ = d.ListTasks("/ws", model.StatusQueue, "", "", "")
	if tasks[0].ID != t3.ID {
		t.Errorf("pos 0: want %s, got %s", t3.ID, tasks[0].ID)
	}
	if tasks[1].ID != t2.ID {
		t.Errorf("pos 1: want %s, got %s", t2.ID, tasks[1].ID)
	}
	if tasks[2].ID != t1.ID {
		t.Errorf("pos 2: want %s, got %s", t1.ID, tasks[2].ID)
	}
}

func TestReorderOptTopBottom(t *testing.T) {
	d := testDB(t)

	t1, _ := d.CreateTask("First", "", model.StatusQueue, "", "/ws", "")
	t2, _ := d.CreateTask("Second", "", model.StatusQueue, "", "/ws", "")
	t3, _ := d.CreateTask("Third", "", model.StatusQueue, "", "/ws", "")

	// Move t3 to top.
	if err := d.ReorderOpt(t3.ID, "", ReorderOpts{Position: -1, Top: true}); err != nil {
		t.Fatalf("ReorderOpt top: %v", err)
	}
	tasks, _ := d.ListTasks("/ws", model.StatusQueue, "", "", "")
	if tasks[0].ID != t3.ID {
		t.Errorf("top: pos 0 want %s, got %s", t3.ID, tasks[0].ID)
	}

	// Move t3 to bottom.
	if err := d.ReorderOpt(t3.ID, "", ReorderOpts{Position: -1, Bottom: true}); err != nil {
		t.Fatalf("ReorderOpt bottom: %v", err)
	}
	tasks, _ = d.ListTasks("/ws", model.StatusQueue, "", "", "")
	if tasks[2].ID != t3.ID {
		t.Errorf("bottom: pos 2 want %s, got %s", t3.ID, tasks[2].ID)
	}

	_ = t1
	_ = t2
}

func TestReorderOptCrossStatus(t *testing.T) {
	d := testDB(t)

	t1, _ := d.CreateTask("Queue1", "", model.StatusQueue, "", "/ws", "")
	b1, _ := d.CreateTask("Backlog1", "", model.StatusBacklog, "", "/ws", "")

	// Move t1 to backlog at top.
	if err := d.ReorderOpt(t1.ID, model.StatusBacklog, ReorderOpts{Position: -1, Top: true}); err != nil {
		t.Fatalf("ReorderOpt cross-status: %v", err)
	}

	queue, _ := d.ListTasks("/ws", model.StatusQueue, "", "", "")
	if len(queue) != 0 {
		t.Errorf("queue should be empty, got %d", len(queue))
	}

	backlog, _ := d.ListTasks("/ws", model.StatusBacklog, "", "", "")
	if len(backlog) != 2 {
		t.Fatalf("expected 2 backlog, got %d", len(backlog))
	}
	if backlog[0].ID != t1.ID {
		t.Errorf("backlog[0]: want %s, got %s", t1.ID, backlog[0].ID)
	}
	if backlog[1].ID != b1.ID {
		t.Errorf("backlog[1]: want %s, got %s", b1.ID, backlog[1].ID)
	}
}

func TestReorderInEpicOpts(t *testing.T) {
	d := testDB(t)

	epic, _ := d.CreateTask("Epic", "", model.StatusQueue, "", "/ws", "")
	d.PromoteToEpic(epic.ID, "")

	c1, _ := d.CreateTask("Child1", "", model.StatusQueue, epic.ID, "/ws", "")
	c2, _ := d.CreateTask("Child2", "", model.StatusQueue, epic.ID, "/ws", "")
	c3, _ := d.CreateTask("Child3", "", model.StatusQueue, epic.ID, "/ws", "")

	// Move c3 before c1.
	if err := d.ReorderInEpicOpts(c3.ID, epic.ID, ReorderOpts{Position: -1, Before: c1.ID}); err != nil {
		t.Fatalf("ReorderInEpicOpts before: %v", err)
	}
	children, _ := d.ListTasks("", "", epic.ID, "", "")
	if children[0].ID != c3.ID {
		t.Errorf("pos 0: want %s, got %s", c3.ID, children[0].ID)
	}
	if children[1].ID != c1.ID {
		t.Errorf("pos 1: want %s, got %s", c1.ID, children[1].ID)
	}
	if children[2].ID != c2.ID {
		t.Errorf("pos 2: want %s, got %s", c2.ID, children[2].ID)
	}

	// Move c1 after c2.
	if err := d.ReorderInEpicOpts(c1.ID, epic.ID, ReorderOpts{Position: -1, After: c2.ID}); err != nil {
		t.Fatalf("ReorderInEpicOpts after: %v", err)
	}
	children, _ = d.ListTasks("", "", epic.ID, "", "")
	if children[0].ID != c3.ID {
		t.Errorf("pos 0: want %s, got %s", c3.ID, children[0].ID)
	}
	if children[1].ID != c2.ID {
		t.Errorf("pos 1: want %s, got %s", c2.ID, children[1].ID)
	}
	if children[2].ID != c1.ID {
		t.Errorf("pos 2: want %s, got %s", c1.ID, children[2].ID)
	}
}

func TestReorderOptBeforeNotFound(t *testing.T) {
	d := testDB(t)

	t1, _ := d.CreateTask("First", "", model.StatusQueue, "", "/ws", "")
	_ = t1

	err := d.ReorderOpt(t1.ID, "", ReorderOpts{Position: -1, Before: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent before ID")
	}
}

func TestSetEpicID(t *testing.T) {
	d := testDB(t)

	epic, _ := d.CreateTask("Epic", "", model.StatusQueue, "", "/ws", "")
	d.PromoteToEpic(epic.ID, "")
	task, _ := d.CreateTask("Standalone", "", model.StatusQueue, "", "/ws", "")

	// Move task into epic.
	if err := d.SetEpicID(task.ID, epic.ID); err != nil {
		t.Fatalf("SetEpicID: %v", err)
	}

	got, _ := d.GetTask(task.ID)
	if got.EpicID != epic.ID {
		t.Errorf("epic_id = %q, want %q", got.EpicID, epic.ID)
	}
}

func TestSetEpicIDClear(t *testing.T) {
	d := testDB(t)

	epic, _ := d.CreateTask("Epic", "", model.StatusQueue, "", "/ws", "")
	d.PromoteToEpic(epic.ID, "")
	child, _ := d.CreateTask("Child", "", model.StatusQueue, epic.ID, "/ws", "")

	// Remove from epic.
	if err := d.SetEpicID(child.ID, ""); err != nil {
		t.Fatalf("SetEpicID clear: %v", err)
	}

	got, _ := d.GetTask(child.ID)
	if got.EpicID != "" {
		t.Errorf("epic_id = %q, want empty", got.EpicID)
	}
}

func TestSetEpicIDAutoPromote(t *testing.T) {
	d := testDB(t)

	// Create two regular tasks.
	target, _ := d.CreateTask("Will become epic", "", model.StatusQueue, "", "/ws", "")
	task, _ := d.CreateTask("Child", "", model.StatusQueue, "", "/ws", "")

	// SetEpicID should auto-promote target.
	if err := d.SetEpicID(task.ID, target.ID); err != nil {
		t.Fatalf("SetEpicID: %v", err)
	}

	got, _ := d.GetTask(target.ID)
	if !got.IsEpic {
		t.Error("expected target to be auto-promoted to epic")
	}
}

func TestListTaskTree(t *testing.T) {
	d := testDB(t)

	// Create an epic with children and a standalone task.
	epic, _ := d.CreateTask("Epic", "", model.StatusQueue, "", "/ws", "")
	d.PromoteToEpic(epic.ID, "")
	c1, _ := d.CreateTask("Child1", "", model.StatusQueue, epic.ID, "/ws", "")
	c2, _ := d.CreateTask("Child2", "", model.StatusQueue, epic.ID, "/ws", "")
	standalone, _ := d.CreateTask("Standalone", "", model.StatusQueue, "", "/ws", "")

	tree, err := d.ListTaskTree("/ws", model.StatusQueue, "", "")
	if err != nil {
		t.Fatalf("ListTaskTree: %v", err)
	}

	// Should have 2 top-level nodes: epic and standalone.
	if len(tree) != 2 {
		t.Fatalf("expected 2 top-level nodes, got %d", len(tree))
	}

	// Find the epic node.
	var epicNode *model.TaskTreeNode
	for i := range tree {
		if tree[i].ID == epic.ID {
			epicNode = &tree[i]
			break
		}
	}
	if epicNode == nil {
		t.Fatal("epic node not found in tree")
	}
	if len(epicNode.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(epicNode.Children))
	}

	childIDs := map[string]bool{epicNode.Children[0].ID: true, epicNode.Children[1].ID: true}
	if !childIDs[c1.ID] || !childIDs[c2.ID] {
		t.Errorf("expected children %s and %s, got %v", c1.ID, c2.ID, childIDs)
	}

	// Standalone should have no children.
	for _, n := range tree {
		if n.ID == standalone.ID {
			if len(n.Children) != 0 {
				t.Errorf("standalone should have 0 children, got %d", len(n.Children))
			}
		}
	}
}

func TestListTaskTreeOrphanedChildren(t *testing.T) {
	d := testDB(t)

	// Epic in backlog, child in queue — child should appear as top-level in queue.
	epic, _ := d.CreateTask("Epic", "", model.StatusBacklog, "", "/ws", "")
	d.PromoteToEpic(epic.ID, "")
	child, _ := d.CreateTask("Orphan Child", "", model.StatusQueue, epic.ID, "/ws", "")

	tree, err := d.ListTaskTree("/ws", model.StatusQueue, "", "")
	if err != nil {
		t.Fatalf("ListTaskTree: %v", err)
	}

	if len(tree) != 1 {
		t.Fatalf("expected 1 top-level node, got %d", len(tree))
	}
	if tree[0].ID != child.ID {
		t.Errorf("expected orphaned child %s, got %s", child.ID, tree[0].ID)
	}
	if tree[0].EpicID != epic.ID {
		t.Errorf("orphaned child should still reference epic %s, got %s", epic.ID, tree[0].EpicID)
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
