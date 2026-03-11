package main

import "testing"

func TestTopTask_Empty(t *testing.T) {
	if got := topTask(nil); got != nil {
		t.Fatalf("expected nil for empty list, got %v", got)
	}
}

func TestTopTask_SortOrder(t *testing.T) {
	tasks := []AtaTask{
		{ID: "b", Title: "B", SortOrder: 2, CreatedAt: "2024-01-01T00:00:00Z"},
		{ID: "a", Title: "A", SortOrder: 1, CreatedAt: "2024-01-02T00:00:00Z"},
		{ID: "c", Title: "C", SortOrder: 3, CreatedAt: "2024-01-01T00:00:00Z"},
	}
	got := topTask(tasks)
	if got.ID != "a" {
		t.Fatalf("expected task 'a' (lowest sort_order), got %q", got.ID)
	}
}

func TestTopTask_TieBreakByCreatedAt(t *testing.T) {
	tasks := []AtaTask{
		{ID: "b", Title: "B", SortOrder: 1, CreatedAt: "2024-01-02T00:00:00Z"},
		{ID: "a", Title: "A", SortOrder: 1, CreatedAt: "2024-01-01T00:00:00Z"},
	}
	got := topTask(tasks)
	if got.ID != "a" {
		t.Fatalf("expected task 'a' (earlier creation), got %q", got.ID)
	}
}
