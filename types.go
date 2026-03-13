package main

import (
	"encoding/json"
	"time"
)

// AtaTask represents a task from ata CLI JSON output.
type AtaTask struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Body      string   `json:"body,omitempty"`
	Status    string   `json:"status"`
	SortOrder int      `json:"sort_order"`
	EpicID    string   `json:"epic_id,omitempty"`
	Workspace string   `json:"workspace"`
	IsEpic    bool     `json:"is_epic"`
	Spec      string   `json:"spec,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	CreatedAt string   `json:"created_at"`
}

// GitWorktree represents a git worktree entry from `git worktree list --porcelain`.
type GitWorktree struct {
	Path   string
	Branch string
	HEAD   string
	IsMain bool
}

// RunnerStatus is the structured output expected from Claude Code at the end of a session.
type RunnerStatus struct {
	Completed      []string `json:"completed"`
	Discovered     []string `json:"discovered"`
	ReviewTasks    []string `json:"review_tasks"`
	DecomposedInto []string `json:"decomposed_into"`
	RemainingReady int      `json:"remaining_ready"`
	Error          *string  `json:"error"`
}

// StreamUsage holds token usage data from a Claude stream result message.
type StreamUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// ClaudeStreamMsg represents a JSON line from claude --output-format stream-json.
type ClaudeStreamMsg struct {
	Type          string         `json:"type"`
	Subtype       string         `json:"subtype,omitempty"`
	SessionID     string         `json:"session_id,omitempty"`
	Content       string         `json:"content,omitempty"`
	ContentBlocks []ContentBlock `json:"content_blocks,omitempty"`
	Message       *MessageObj    `json:"message,omitempty"`
	TotalCostUSD  float64        `json:"total_cost_usd,omitempty"`
	DurationMS    int            `json:"duration_ms,omitempty"`
	DurationAPI   int            `json:"duration_api_ms,omitempty"`
	Usage         *StreamUsage   `json:"usage,omitempty"`
	NumTurns      int            `json:"num_turns,omitempty"`
}

// ContentBlock is a block within a Claude stream message.
// It can represent text, tool_use, or tool_result content.
type ContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`  // tool_use: tool name
	ID    string          `json:"id,omitempty"`    // tool_use: call ID
	Input json.RawMessage `json:"input,omitempty"` // tool_use: tool input
}

// MessageObj wraps content blocks in a Claude stream result message.
type MessageObj struct {
	Content []ContentBlock `json:"content,omitempty"`
}

// SessionResult holds everything collected from a single Claude session.
type SessionResult struct {
	SessionID    string
	Status       *RunnerStatus
	RawOutput    string
	Error        error
	UserQuit     bool
	UserSkipped  bool
	TotalCostUSD float64
	InputTokens  int
	OutputTokens int
	NumTurns     int
	DurationMS   int
}

// RunStats tracks cumulative stats across the orchestration run.
type RunStats struct {
	TasksCompleted      int
	Discovered          int
	ReviewTasks         int
	Decomposed          int
	SessionsRun         int
	Errors              int
	TriageSessions      int
	TriageSkipped       int
	RecoveredTasks      int
	EpicsClosed         int
	StartedAt           time.Time
	TotalCostUSD        float64
	TotalInput          int
	TotalOutput         int
}

// ReviewConfig holds configuration for the rev subcommand.
type ReviewConfig struct {
	Base      string
	MaxRounds int
	Yolo      bool
	LogDir    string
}

// ReviewStatus is the structured output from a review session.
type ReviewStatus struct {
	TasksFiled   []ReviewTask `json:"tasks_filed"`
	FixesApplied []string     `json:"fixes_applied"`
	Summary      string       `json:"summary"`
	Severity     string       `json:"severity"`
	Error        *string      `json:"error"`
}

// ReviewTask represents a task filed during a review round.
type ReviewTask struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
}

// ReviewRound records the outcome of a single review iteration.
type ReviewRound struct {
	Number     int
	Status     *ReviewStatus
	TasksFiled []ReviewTask
	HeadSHA    string
}

// TriageEvidence holds signals collected after a session for post-session triage.
type TriageEvidence struct {
	TaskID         string
	TaskTitle      string
	PreSHA         string
	PostSHA        string
	CommitCount    int
	CommitSummary  string // git log --oneline
	DiffStats      string // git diff --stat
	HasUncommitted bool
	TasksCreated   []AtaTask // tasks created during session
	TaskStatus     string    // from ata show
	SessionID      string
	HadError       bool
}

// TriageOutcome represents the triage decision for a session without structured output.
type TriageOutcome int

const (
	TriageComplete   TriageOutcome = iota // heuristic-only: ata show confirms closed
	TriagePartial                         // commits/tasks exist, add context comment
	TriageNoProgress                      // nothing happened
	TriageNeedsAgent                      // ambiguous, spawn Layer 2
)

// TriageResult holds the outcome from triage (heuristic or agent).
type TriageResult struct {
	Outcome      TriageOutcome
	Reason       string
	Comment      string
	AgentSpawned bool // true if Layer 2 triage agent was used
	// Cost fields from the triage agent session (zero if heuristic-only).
	TotalCostUSD float64
	InputTokens  int
	OutputTokens int
}

// TriageStatus is the structured output from a triage agent session.
type TriageStatus struct {
	Outcome string  `json:"outcome"`
	Comment string  `json:"comment"`
	Error   *string `json:"error"`
}

// ReviewStats tracks cumulative stats for the review run.
type ReviewStats struct {
	RoundsRun  int
	TotalTasks int
	TotalFixes int
	StopReason string
	CommitSweep bool
	StartedAt  time.Time
}
