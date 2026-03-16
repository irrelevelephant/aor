package main

import (
	"strings"
	"testing"
)

func TestBuildPullPrompt(t *testing.T) {
	task := &AtaTask{
		ID:     "abc",
		Title:  "Implement feature X",
		Body:   "Details about feature X",
		EpicID: "ep1",
		Spec:   "## Locked Decisions\nUse REST not gRPC",
	}

	prompt := buildPullPrompt(task, "/tmp/worktree", "Epic spec content", depthFull)

	checks := []string{
		"task abc",
		"Implement feature X",
		"Details about feature X",
		"epic ep1",
		"Epic spec content",
		"/tmp/worktree",
		"Phase 0: Deep Interview",
		"Phase 1: Research and Plan",
		"Phase 2: Review with User",
		"Phase 3a: Execute Directly",
		"Phase 3b: Decompose into Subtasks",
		"Locked Decisions",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("pull prompt missing %q", check)
		}
	}

	// depthLight should have Quick Clarification, not Deep Interview.
	lightPrompt := buildPullPrompt(task, "", "", depthLight)
	if !strings.Contains(lightPrompt, "Quick Clarification") {
		t.Error("light depth should include Quick Clarification")
	}
	if strings.Contains(lightPrompt, "Deep Interview") {
		t.Error("light depth should not include Deep Interview")
	}

	// depthSkip should have neither.
	skipPrompt := buildPullPrompt(task, "", "", depthSkip)
	if strings.Contains(skipPrompt, "Phase 0") {
		t.Error("skip depth should not include Phase 0")
	}
}

func TestBuildMergePrompt(t *testing.T) {
	worktrees := []mergeWorktreeInfo{
		{
			GitWorktree: GitWorktree{Path: "/tmp/wt1", Branch: "feature-1"},
			Commits:     "abc123 Add feature 1",
		},
		{
			GitWorktree: GitWorktree{Path: "/tmp/wt2", Branch: "feature-2"},
		},
	}
	mainWT := GitWorktree{Path: "/repo", Branch: "main"}

	prompt := buildMergePrompt(worktrees, mainWT)

	checks := []string{
		"main",
		"/repo",
		"/tmp/wt1",
		"feature-1",
		"abc123 Add feature 1",
		"/tmp/wt2",
		"No unique commits",
		"git merge",
		"git worktree remove",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("merge prompt missing %q", check)
		}
	}
}

func TestBuildReviewPrompt(t *testing.T) {
	diff := "+func newFunc() {}\n-func oldFunc() {}"
	priorTasks := []ReviewTask{
		{ID: "t1", Title: "Fix bug"},
	}

	prompt := buildReviewPrompt(diff, "abc123", 2, priorTasks, "review", "epic-42", "/tmp/ws")

	checks := []string{
		"abc123",
		"+func newFunc",
		"-func oldFunc",
		"Prior review rounds",
		"t1: Fix bug",
		"REVIEW_STATUS",
		"review",
		`--epic "epic-42"`,
		`--workspace "/tmp/ws"`,
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("review prompt missing %q", check)
		}
	}

	// Round 1 should not show prior tasks.
	prompt1 := buildReviewPrompt(diff, "abc123", 1, nil, "", "", "")
	if strings.Contains(prompt1, "Prior review rounds") {
		t.Error("round 1 should not show prior tasks")
	}
}

func TestBuildSpecPrompt(t *testing.T) {
	specs := []string{"# Feature Spec\nBuild a widget"}

	prompt := buildSpecPrompt(specs, "/my/workspace")

	checks := []string{
		"Feature Spec",
		"Build a widget",
		"/my/workspace",
		"Phase 1: Research",
		"Phase 2: Refine Spec",
		"Phase 3: Propose Execution Plan",
		"ata create",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("spec prompt missing %q", check)
		}
	}

	// Multi-spec should mention cross-epic.
	multiPrompt := buildSpecPrompt([]string{"Spec A", "Spec B"}, "")
	if !strings.Contains(multiPrompt, "Cross-Epic") {
		t.Error("multi-spec prompt should mention cross-epic dependencies")
	}
}
