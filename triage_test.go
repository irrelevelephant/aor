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
				NumTurns:       5,
				MaxTurns:       100,
			},
			want: TriageNoProgress,
		},
		{
			name: "commits exist, high turn usage",
			evidence: TriageEvidence{
				CommitCount: 3,
				NumTurns:    80,
				MaxTurns:    100,
			},
			want: TriagePartial,
		},
		{
			name: "commits exist, low turns, session error",
			evidence: TriageEvidence{
				CommitCount: 2,
				NumTurns:    20,
				MaxTurns:    100,
				HadError:    true,
			},
			want: TriagePartial,
		},
		{
			name: "commits exist, low turns, no error — ambiguous",
			evidence: TriageEvidence{
				CommitCount: 2,
				NumTurns:    20,
				MaxTurns:    100,
				HadError:    false,
			},
			want: TriageNeedsAgent,
		},
		{
			name: "tasks created but no commits",
			evidence: TriageEvidence{
				CommitCount:  0,
				TasksCreated: []AtaTask{{ID: "TSK-1", Title: "subtask"}},
				NumTurns:     30,
				MaxTurns:     100,
			},
			want: TriagePartial,
		},
		{
			name: "only uncommitted changes",
			evidence: TriageEvidence{
				CommitCount:    0,
				HasUncommitted: true,
				NumTurns:       10,
				MaxTurns:       100,
			},
			want: TriagePartial,
		},
		{
			name: "commits at exactly 50% boundary — partial",
			evidence: TriageEvidence{
				CommitCount: 1,
				NumTurns:    51,
				MaxTurns:    100,
			},
			want: TriagePartial,
		},
		{
			name: "commits at exactly 50% turns — needs agent",
			evidence: TriageEvidence{
				CommitCount: 1,
				NumTurns:    50,
				MaxTurns:    100,
			},
			want: TriageNeedsAgent,
		},
		{
			name: "zero max turns — no division by zero",
			evidence: TriageEvidence{
				CommitCount: 1,
				NumTurns:    0,
				MaxTurns:    0,
			},
			want: TriageNeedsAgent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := triageHeuristic(&tt.evidence)
			if got.Outcome != tt.want {
				t.Errorf("triageHeuristic() outcome = %s, want %s (reason: %s)",
					triageOutcomeName(got.Outcome), triageOutcomeName(tt.want), got.Reason)
			}
		})
	}
}

func TestTriageOutcomeName(t *testing.T) {
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
		got := triageOutcomeName(tt.outcome)
		if got != tt.want {
			t.Errorf("triageOutcomeName(%d) = %q, want %q", tt.outcome, got, tt.want)
		}
	}
}

func TestBuildTriageComment(t *testing.T) {
	ev := &TriageEvidence{
		TaskID:      "TSK-42",
		TaskTitle:   "Fix the widget",
		NumTurns:    75,
		MaxTurns:    100,
		CommitCount: 3,
		CommitSummary: "abc1234 first commit\ndef5678 second commit\nghi9012 third commit",
		DiffStats:   " file1.go | 10 +++\n file2.go | 5 --",
		TasksCreated: []AtaTask{
			{ID: "TSK-43", Title: "Subtask A"},
		},
	}
	result := &TriageResult{
		Outcome: TriagePartial,
		Reason:  "commits exist (3), used 75% of turns",
	}

	comment := buildTriageComment(ev, result)

	// Verify key sections are present.
	for _, want := range []string{
		"## Post-Session Triage",
		"**Outcome:** partial",
		"Turns used: 75/100 (75%)",
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
}
