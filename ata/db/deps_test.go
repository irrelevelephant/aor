package db

import (
	"os"
	"testing"

	"aor/ata/model"
)

func TestPropagateDeps(t *testing.T) {
	d := testDB(t)

	a, _ := d.CreateTask("A", "", model.StatusQueue, "", "")
	b, _ := d.CreateTask("B", "", model.StatusQueue, "", "")
	c, _ := d.CreateTask("C", "", model.StatusQueue, "", "")

	// B depends on A (A blocks B).
	if err := d.AddDep(b.ID, a.ID); err != nil {
		t.Fatalf("AddDep B→A: %v", err)
	}

	// Propagate: anything depending on A should also depend on C.
	added, err := d.PropagateDeps(a.ID, c.ID)
	if err != nil {
		t.Fatalf("PropagateDeps: %v", err)
	}
	if added != 1 {
		t.Errorf("added = %d, want 1", added)
	}

	// Verify B now depends on C.
	blockers, err := d.GetBlockers(b.ID, false)
	if err != nil {
		t.Fatalf("GetBlockers: %v", err)
	}
	found := false
	for _, bl := range blockers {
		if bl.ID == c.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("B should depend on C after propagation")
	}
}

func TestPropagateDeps_Cycle(t *testing.T) {
	d := testDB(t)

	a, _ := d.CreateTask("A", "", model.StatusQueue, "", "")
	b, _ := d.CreateTask("B", "", model.StatusQueue, "", "")
	c, _ := d.CreateTask("C", "", model.StatusQueue, "", "")

	// B depends on A.
	d.AddDep(b.ID, a.ID)
	// C depends on B.
	d.AddDep(c.ID, b.ID)

	// Propagate A→C: would try to add B→C, but C already depends on B,
	// and adding B→C would create a cycle. Should be skipped.
	added, err := d.PropagateDeps(a.ID, c.ID)
	if err != nil {
		t.Fatalf("PropagateDeps: %v", err)
	}
	if added != 0 {
		t.Errorf("added = %d, want 0 (cycle should be skipped)", added)
	}
}

func TestPropagateDeps_NoDependents(t *testing.T) {
	d := testDB(t)

	a, _ := d.CreateTask("A", "", model.StatusQueue, "", "")
	c, _ := d.CreateTask("C", "", model.StatusQueue, "", "")

	// A has no dependents.
	added, err := d.PropagateDeps(a.ID, c.ID)
	if err != nil {
		t.Fatalf("PropagateDeps: %v", err)
	}
	if added != 0 {
		t.Errorf("added = %d, want 0", added)
	}
}

func TestPropagateDeps_Multiple(t *testing.T) {
	d := testDB(t)

	a, _ := d.CreateTask("A", "", model.StatusQueue, "", "")
	b1, _ := d.CreateTask("B1", "", model.StatusQueue, "", "")
	b2, _ := d.CreateTask("B2", "", model.StatusQueue, "", "")
	c, _ := d.CreateTask("C", "", model.StatusQueue, "", "")

	// B1 and B2 both depend on A.
	d.AddDep(b1.ID, a.ID)
	d.AddDep(b2.ID, a.ID)

	added, err := d.PropagateDeps(a.ID, c.ID)
	if err != nil {
		t.Fatalf("PropagateDeps: %v", err)
	}
	if added != 2 {
		t.Errorf("added = %d, want 2", added)
	}

	// Both B1 and B2 should now depend on C.
	for _, bID := range []string{b1.ID, b2.ID} {
		blockers, _ := d.GetBlockers(bID, false)
		found := false
		for _, bl := range blockers {
			if bl.ID == c.ID {
				found = true
			}
		}
		if !found {
			t.Errorf("%s should depend on C after propagation", bID)
		}
	}
}

func TestEpicDepInheritance(t *testing.T) {
	d := testDB(t)

	// Epic E with child task C; E depends on a separate task D.
	e, _ := d.CreateTask("Epic", "", model.StatusQueue, "", "")
	d.PromoteToEpic(e.ID)
	c, _ := d.CreateTask("Child", "", model.StatusQueue, e.ID, "")
	dep, _ := d.CreateTask("Dep", "", model.StatusQueue, "", "")
	if err := d.AddDep(e.ID, dep.ID); err != nil {
		t.Fatalf("AddDep epic→dep: %v", err)
	}

	// Child should be excluded from ReadyTasks because its epic is blocked.
	ready, _ := d.ReadyTasks("", "", 0)
	for _, r := range ready {
		if r.ID == c.ID {
			t.Errorf("child %s should not be ready while epic dep is open", c.ID)
		}
	}

	// BlockedTaskIDs should flag the child via inheritance.
	ids, _ := d.BlockedTaskIDs([]string{c.ID, dep.ID})
	if !ids[c.ID] {
		t.Errorf("child %s should be flagged as blocked (inherited from epic)", c.ID)
	}

	// EffectiveBlockers on the child returns the epic's dep.
	eff, err := d.EffectiveBlockers(c.ID)
	if err != nil {
		t.Fatalf("EffectiveBlockers: %v", err)
	}
	if len(eff) != 1 || eff[0].ID != dep.ID {
		t.Errorf("EffectiveBlockers(child) = %v, want [%s]", eff, dep.ID)
	}

	// GetBlockers on the child still returns nothing (no direct dep).
	direct, _ := d.GetBlockers(c.ID, true)
	if len(direct) != 0 {
		t.Errorf("GetBlockers(child) = %v, want empty (direct deps only)", direct)
	}

	// Claiming the child should be rejected.
	if _, err := d.ClaimTask(c.ID, os.Getpid(), "test"); err == nil {
		t.Error("ClaimTask on child should fail when epic dep is open")
	}

	// Closing the dep unblocks the child.
	if _, err := d.CloseTask(dep.ID, "done"); err != nil {
		t.Fatalf("CloseTask dep: %v", err)
	}
	ready, _ = d.ReadyTasks("", "", 0)
	found := false
	for _, r := range ready {
		if r.ID == c.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("child %s should be ready after dep closes", c.ID)
	}
}

func TestEpicDepInheritance_Nested(t *testing.T) {
	d := testDB(t)

	// root → sub → leaf, with the *root* depending on an external task.
	root, _, leaf := makeNestedEpic(t, d)
	dep, _ := d.CreateTask("Dep", "", model.StatusQueue, "", "")
	if err := d.AddDep(root.ID, dep.ID); err != nil {
		t.Fatalf("AddDep root→dep: %v", err)
	}

	// Leaf should inherit through two levels of nesting.
	ids, _ := d.BlockedTaskIDs([]string{leaf.ID})
	if !ids[leaf.ID] {
		t.Errorf("leaf %s should be blocked via root epic", leaf.ID)
	}

	ready, _ := d.ReadyTasks(root.ID, "", 0)
	if len(ready) != 0 {
		t.Errorf("ready under blocked root = %v, want empty", ready)
	}

	// After dep closes, leaf is ready.
	d.CloseTask(dep.ID, "done")
	ready, _ = d.ReadyTasks(root.ID, "", 0)
	if len(ready) != 1 || ready[0].ID != leaf.ID {
		t.Errorf("ready after unblock = %v, want [%s]", ready, leaf.ID)
	}
}

func TestPropagateDeps_Duplicate(t *testing.T) {
	d := testDB(t)

	a, _ := d.CreateTask("A", "", model.StatusQueue, "", "")
	b, _ := d.CreateTask("B", "", model.StatusQueue, "", "")
	c, _ := d.CreateTask("C", "", model.StatusQueue, "", "")

	d.AddDep(b.ID, a.ID)
	// B already depends on C.
	d.AddDep(b.ID, c.ID)

	// Propagate succeeds (INSERT OR IGNORE is a no-op for the existing dep).
	added, err := d.PropagateDeps(a.ID, c.ID)
	if err != nil {
		t.Fatalf("PropagateDeps: %v", err)
	}
	if added != 1 {
		t.Errorf("added = %d, want 1 (duplicate is silently accepted by AddDep)", added)
	}

	// Verify B still has exactly 2 blockers (A and C), not a duplicate C.
	blockers, _ := d.GetBlockers(b.ID, false)
	if len(blockers) != 2 {
		t.Errorf("blockers = %d, want 2", len(blockers))
	}
}
