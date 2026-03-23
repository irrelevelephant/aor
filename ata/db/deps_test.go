package db

import (
	"testing"

	"aor/ata/model"
)

func TestPropagateDeps(t *testing.T) {
	d := testDB(t)

	a, _ := d.CreateTask("A", "", model.StatusQueue, "", "/ws", "")
	b, _ := d.CreateTask("B", "", model.StatusQueue, "", "/ws", "")
	c, _ := d.CreateTask("C", "", model.StatusQueue, "", "/ws", "")

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

	a, _ := d.CreateTask("A", "", model.StatusQueue, "", "/ws", "")
	b, _ := d.CreateTask("B", "", model.StatusQueue, "", "/ws", "")
	c, _ := d.CreateTask("C", "", model.StatusQueue, "", "/ws", "")

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

	a, _ := d.CreateTask("A", "", model.StatusQueue, "", "/ws", "")
	c, _ := d.CreateTask("C", "", model.StatusQueue, "", "/ws", "")

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

	a, _ := d.CreateTask("A", "", model.StatusQueue, "", "/ws", "")
	b1, _ := d.CreateTask("B1", "", model.StatusQueue, "", "/ws", "")
	b2, _ := d.CreateTask("B2", "", model.StatusQueue, "", "/ws", "")
	c, _ := d.CreateTask("C", "", model.StatusQueue, "", "/ws", "")

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

func TestPropagateDeps_Duplicate(t *testing.T) {
	d := testDB(t)

	a, _ := d.CreateTask("A", "", model.StatusQueue, "", "/ws", "")
	b, _ := d.CreateTask("B", "", model.StatusQueue, "", "/ws", "")
	c, _ := d.CreateTask("C", "", model.StatusQueue, "", "/ws", "")

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
