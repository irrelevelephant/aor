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
	err := checkScope("claim", "T-1", "wt-abc", []string{})
	if err == nil {
		t.Fatal("expected error for empty labels slice")
	}
	if !errors.Is(err, ErrScopeViolation) {
		t.Fatalf("expected ErrScopeViolation, got: %v", err)
	}
}

func TestCheckScope_NilLabels(t *testing.T) {
	err := checkScope("claim", "T-1", "wt-abc", nil)
	if err == nil {
		t.Fatal("expected error for nil labels")
	}
	if !errors.Is(err, ErrScopeViolation) {
		t.Fatalf("expected ErrScopeViolation, got: %v", err)
	}
}
