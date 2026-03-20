package main

import (
	"strings"
	"testing"
)

func TestTriageHeuristic(t *testing.T) {
	tests := []struct {
		name     string
		evidence TriageEvidence
		want     TriageOutcome
	}{
		{
			name: "task already closed",
			evidence: TriageEvidence{
				TaskStatus: "closed",
			},
			want: TriageComplete,
		},
		{
			name: "no progress at all",
			evidence: TriageEvidence{
				CommitCount:    0,
				HasUncommitted: false,
			},
			want: TriageNoProgress,
		},
		{
			name: "commits exist, no error — needs agent",
			evidence: TriageEvidence{
				CommitCount: 3,
			},
			want: TriageNeedsAgent,
		},
		{
			name: "commits exist, session error — partial",
			evidence: TriageEvidence{
				CommitCount: 2,
				HadError:    true,
			},
			want: TriagePartial,
		},
		{
			name: "commits exist, no error — ambiguous",
			evidence: TriageEvidence{
				CommitCount: 2,
				HadError:    false,
			},
			want: TriageNeedsAgent,
		},
		{
			name: "tasks created but no commits",
			evidence: TriageEvidence{
				CommitCount:  0,
				TasksCreated: []AtaTask{{ID: "TSK-1", Title: "subtask"}},
			},
			want: TriagePartial,
		},
		{
			name: "only uncommitted changes",
			evidence: TriageEvidence{
				CommitCount:    0,
				HasUncommitted: true,
			},
			want: TriagePartial,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := triageHeuristic(&tt.evidence)
			if got.Outcome != tt.want {
				t.Errorf("triageHeuristic() outcome = %s, want %s (reason: %s)",
					got.Outcome, tt.want, got.Reason)
			}
		})
	}
}

func TestTriageOutcomeString(t *testing.T) {
	tests := []struct {
		outcome TriageOutcome
		want    string
	}{
		{TriageComplete, "complete"},
		{TriagePartial, "partial"},
		{TriageNoProgress, "no_progress"},
		{TriageNeedsAgent, "needs_agent"},
		{TriageOutcome(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.outcome.String()
		if got != tt.want {
			t.Errorf("TriageOutcome(%d).String() = %q, want %q", tt.outcome, got, tt.want)
		}
	}
}

func TestBuildTriageComment(t *testing.T) {
	ev := &TriageEvidence{
		TaskID:      "TSK-42",
		TaskTitle:   "Fix the widget",
		CommitCount: 3,
		CommitSummary: "abc1234 first commit\ndef5678 second commit\nghi9012 third commit",
		DiffStats:   " file1.go | 10 +++\n file2.go | 5 --",
		TasksCreated: []AtaTask{
			{ID: "TSK-43", Title: "Subtask A"},
		},
	}
	result := &TriageResult{
		Outcome: TriagePartial,
		Reason:  "commits exist (3) but session errored",
	}

	comment := buildTriageComment(ev, result)

	// Verify key sections are present.
	for _, want := range []string{
		"## Post-Session Triage",
		"**Outcome:** partial",
		"Commits: 3",
		"abc1234 first commit",
		"file1.go",
		"Tasks created: 1",
		"TSK-43: Subtask A",
	} {
		if !strings.Contains(comment, want) {
			t.Errorf("buildTriageComment() missing %q in output:\n%s", want, comment)
		}
	}

	// Verify turns line is NOT present.
	if strings.Contains(comment, "Turns used") {
		t.Errorf("buildTriageComment() should not contain 'Turns used', got:\n%s", comment)
	}
}
