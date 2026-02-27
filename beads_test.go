package main

import (
	"errors"
	"testing"
)

func TestCheckScope_EmptyScope(t *testing.T) {
	if err := checkScope("claim", "T-1", "", []string{"foo"}); err != nil {
		t.Fatalf("empty scope should pass, got: %v", err)
	}
}

func TestCheckScope_MatchingLabel(t *testing.T) {
	if err := checkScope("claim", "T-1", "wt-abc", []string{"wt-abc", "other"}); err != nil {
		t.Fatalf("matching label should pass, got: %v", err)
	}
}

func TestCheckScope_MissingLabel(t *testing.T) {
	err := checkScope("claim", "T-1", "wt-abc", []string{"other"})
	if err == nil {
		t.Fatal("expected error for missing scope label")
	}
	if !errors.Is(err, ErrScopeViolation) {
		t.Fatalf("expected ErrScopeViolation, got: %v", err)
	}
}

func TestCheckScope_EmptyLabels(t *testing.T) {
	// Empty labels = unknown (bd ready --json doesn't populate labels).
	// Trust upstream filtering; do not reject.
	if err := checkScope("claim", "T-1", "wt-abc", []string{}); err != nil {
		t.Fatalf("empty labels should pass (unknown), got: %v", err)
	}
}

func TestCheckScope_NilLabels(t *testing.T) {
	// Nil labels = unknown; same as empty.
	if err := checkScope("claim", "T-1", "wt-abc", nil); err != nil {
		t.Fatalf("nil labels should pass (unknown), got: %v", err)
	}
}
