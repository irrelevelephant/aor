package main

import (
	"fmt"
	"strings"
	"time"
)

// gatherTriageEvidence collects git and task signals from the session.
func gatherTriageEvidence(taskID, taskTitle, preSHA string,
	sessionStart time.Time, result *SessionResult, cfg *Config) *TriageEvidence {

	ev := &TriageEvidence{
		TaskID:    taskID,
		TaskTitle: taskTitle,
		PreSHA:    preSHA,
		SessionID: result.SessionID,
		HadError:  result.Error != nil,
	}

	// Current HEAD.
	if postSHA, err := headSHA(); err == nil {
		ev.PostSHA = postSHA
	}

	// Commits between pre and post SHA.
	if ev.PreSHA != "" && ev.PostSHA != "" && ev.PreSHA != ev.PostSHA {
		if count, err := commitCountBetween(ev.PreSHA, ev.PostSHA); err == nil {
			ev.CommitCount = count
		}
		if summary, err := commitsBetween(ev.PreSHA, ev.PostSHA); err == nil {
			ev.CommitSummary = summary
		}
		if stats, err := diffStatBetween(ev.PreSHA, ev.PostSHA); err == nil {
			ev.DiffStats = stats
		}
	}

	// Uncommitted changes.
	ev.HasUncommitted = hasUncommittedChanges()

	// Task status from ata.
	if task, err := getTaskStatus(taskID); err == nil {
		ev.TaskStatus = task.Status
	}

	// Tasks created during session.
	if tasks, err := getTasksCreatedAfter(sessionStart, cfg.Workspace); err == nil {
		ev.TasksCreated = tasks
	}

	return ev
}

// triageHeuristic examines evidence and returns an outcome.
// Returns TriageNeedsAgent when signals are ambiguous.
func triageHeuristic(ev *TriageEvidence) *TriageResult {
	// Already closed — nothing to do.
	if ev.TaskStatus == "closed" {
		return &TriageResult{
			Outcome: TriageComplete,
			Reason:  "task already closed (ata show confirms)",
		}
	}

	hasCommits := ev.CommitCount > 0
	hasTasks := len(ev.TasksCreated) > 0

	// No progress at all — nothing happened.
	if !hasCommits && !hasTasks && !ev.HasUncommitted {
		return &TriageResult{
			Outcome: TriageNoProgress,
			Reason:  "no commits, no tasks created, no uncommitted changes",
		}
	}

	// Commits exist + error — crashed mid-work.
	if hasCommits && ev.HadError {
		return &TriageResult{
			Outcome: TriagePartial,
			Reason:  fmt.Sprintf("commits exist (%d) but session errored", ev.CommitCount),
		}
	}

	// Commits exist, no error, task not closed — ambiguous, needs agent.
	if hasCommits && !ev.HadError {
		return &TriageResult{
			Outcome: TriageNeedsAgent,
			Reason:  fmt.Sprintf("commits exist (%d), no error, task not closed — ambiguous", ev.CommitCount),
		}
	}

	// Tasks created during session but no commits — planning happened, no code.
	if hasTasks {
		return &TriageResult{
			Outcome: TriagePartial,
			Reason:  fmt.Sprintf("%d tasks created but no commits", len(ev.TasksCreated)),
		}
	}

	// Fallback: uncommitted changes only (all other cases exhausted above).
	return &TriageResult{
		Outcome: TriagePartial,
		Reason:  "uncommitted changes present but no commits or tasks",
	}
}

// buildTriageComment creates a markdown summary for attachment to the task.
func buildTriageComment(ev *TriageEvidence, outcome *TriageResult) string {
	var b strings.Builder

	b.WriteString("## Post-Session Triage\n\n")
	b.WriteString(fmt.Sprintf("**Outcome:** %s\n", triageOutcomeName(outcome.Outcome)))
	b.WriteString(fmt.Sprintf("**Reason:** %s\n\n", outcome.Reason))

	b.WriteString("### Evidence\n\n")

	if ev.CommitCount > 0 {
		b.WriteString(fmt.Sprintf("- Commits: %d\n", ev.CommitCount))
		if ev.CommitSummary != "" {
			b.WriteString("- Commit log:\n```\n")
			b.WriteString(ev.CommitSummary)
			b.WriteString("\n```\n")
		}
		if ev.DiffStats != "" {
			b.WriteString("- Diff stats:\n```\n")
			b.WriteString(ev.DiffStats)
			b.WriteString("\n```\n")
		}
	} else {
		b.WriteString("- Commits: none\n")
	}

	if ev.HasUncommitted {
		b.WriteString("- Uncommitted changes: yes\n")
	}

	if len(ev.TasksCreated) > 0 {
		b.WriteString(fmt.Sprintf("- Tasks created: %d\n", len(ev.TasksCreated)))
		for _, task := range ev.TasksCreated {
			b.WriteString(fmt.Sprintf("  - %s: %s\n", task.ID, task.Title))
		}
	}

	if ev.HadError {
		b.WriteString("- Session had error: yes\n")
	}

	if outcome.Comment != "" {
		b.WriteString(fmt.Sprintf("\n### Agent Notes\n\n%s\n", outcome.Comment))
	}

	return b.String()
}

// buildTriagePrompt constructs a prompt for the triage agent (Layer 2).
func buildTriagePrompt(ev *TriageEvidence) string {
	var b strings.Builder

	b.WriteString("You are a triage agent examining the aftermath of an agent session that ended without structured output.\n\n")
	b.WriteString(fmt.Sprintf("Task: %s — %s\n\n", ev.TaskID, ev.TaskTitle))

	b.WriteString("## Evidence\n\n")
	b.WriteString(fmt.Sprintf("- Task status (ata show): %s\n", ev.TaskStatus))
	b.WriteString(fmt.Sprintf("- Commits: %d\n", ev.CommitCount))

	if ev.CommitSummary != "" {
		b.WriteString("- Commit log:\n```\n")
		b.WriteString(ev.CommitSummary)
		b.WriteString("\n```\n")
	}
	if ev.DiffStats != "" {
		b.WriteString("- Diff stats:\n```\n")
		b.WriteString(ev.DiffStats)
		b.WriteString("\n```\n")
	}
	if ev.HasUncommitted {
		b.WriteString("- Uncommitted working tree changes: yes\n")
	}
	if len(ev.TasksCreated) > 0 {
		b.WriteString(fmt.Sprintf("- Tasks created during session: %d\n", len(ev.TasksCreated)))
		for _, task := range ev.TasksCreated {
			b.WriteString(fmt.Sprintf("  - %s: %s\n", task.ID, task.Title))
		}
	}
	if ev.HadError {
		b.WriteString("- Session error: yes\n")
	}

	b.WriteString("\n## Your Task\n\n")
	b.WriteString("Examine the evidence above. You may also:\n")
	b.WriteString("- Run `ata show " + ev.TaskID + " --json` to check current task state\n")
	b.WriteString("- Examine git log and diff to understand what the previous agent did\n")
	b.WriteString("- Read files to check the state of the work\n\n")

	b.WriteString("**You can ONLY add comments.** You CANNOT close tasks, create subtasks, or modify code.\n\n")

	b.WriteString("Determine:\n")
	b.WriteString("1. What did the previous agent accomplish?\n")
	b.WriteString("2. Why did it stop without producing structured output?\n")
	b.WriteString("3. What should the next agent know to continue effectively?\n\n")

	b.WriteString("Output your assessment as a TRIAGE_STATUS sentinel on its own line:\n\n")
	b.WriteString(`TRIAGE_STATUS:{"outcome": "<complete|partial|no_progress>", "comment": "<markdown summary for next agent>", "error": null}`)
	b.WriteString("\n\n")
	b.WriteString("- outcome=complete: the task appears done (code is in place, tests pass)\n")
	b.WriteString("- outcome=partial: meaningful progress was made\n")
	b.WriteString("- outcome=no_progress: nothing useful happened\n")

	return b.String()
}

// runTriage orchestrates both layers: heuristic first, agent if needed.
func runTriage(ev *TriageEvidence, cfg *Config, log *Logger,
	stdinCh <-chan string) *TriageResult {

	result := triageHeuristic(ev)
	log.Log("Triage heuristic: %s — %s", triageOutcomeName(result.Outcome), result.Reason)

	if result.Outcome != TriageNeedsAgent {
		if result.Outcome == TriagePartial {
			result.Comment = buildTriageComment(ev, result)
		}
		return result
	}

	// Layer 2: spawn triage agent.
	log.Log("Triage heuristic inconclusive — spawning triage agent...")

	triagePrompt := buildTriagePrompt(ev)
	triageCfg := &Config{
		Yolo:    cfg.Yolo,
		LogDir:  cfg.LogDir,
		WorkDir: cfg.WorkDir,
	}

	fmt.Printf("\n%s─── Triage: %s ──────────────────────────────────────%s\n\n",
		cYellow, ev.TaskID, cReset)

	triageResult := runSession(triageCfg, log, triagePrompt, stdinCh)

	// Log triage session costs.
	if triageResult.InputTokens > 0 || triageResult.OutputTokens > 0 {
		log.Log("Triage usage: %s input + %s output tokens, $%.4f",
			formatTokens(triageResult.InputTokens), formatTokens(triageResult.OutputTokens),
			triageResult.TotalCostUSD)
	}

	// Helper to populate cost fields from the triage session.
	setCosts := func(r *TriageResult) {
		r.TotalCostUSD = triageResult.TotalCostUSD
		r.InputTokens = triageResult.InputTokens
		r.OutputTokens = triageResult.OutputTokens
	}

	// Parse TRIAGE_STATUS sentinel.
	triageStatus := parseSentinelJSON[TriageStatus](triageResult.RawOutput, "TRIAGE_STATUS:")
	if triageStatus != nil {
		outcome := TriagePartial // default
		switch triageStatus.Outcome {
		case "complete":
			if task, err := getTaskStatus(ev.TaskID); err == nil && task.Status == "closed" {
				outcome = TriageComplete
			} else {
				outcome = TriagePartial
			}
		case "no_progress":
			outcome = TriageNoProgress
		case "partial":
			outcome = TriagePartial
		}

		agentResult := &TriageResult{
			Outcome:      outcome,
			Reason:       fmt.Sprintf("triage agent: %s", triageStatus.Outcome),
			Comment:      triageStatus.Comment,
			AgentSpawned: true,
		}
		setCosts(agentResult)

		if outcome == TriagePartial {
			agentResult.Comment = buildTriageComment(ev, agentResult)
		}

		return agentResult
	}

	// Triage agent also failed to produce sentinel — fall back to partial.
	log.Log("%sTriage agent did not produce TRIAGE_STATUS sentinel — defaulting to partial%s", cYellow, cReset)
	fallback := &TriageResult{
		Outcome:      TriagePartial,
		Reason:       "triage agent failed to produce sentinel",
		AgentSpawned: true,
	}
	setCosts(fallback)
	fallback.Comment = buildTriageComment(ev, fallback)
	return fallback
}

// triageOutcomeName returns a human-readable name for a TriageOutcome.
func triageOutcomeName(o TriageOutcome) string {
	switch o {
	case TriageComplete:
		return "complete"
	case TriagePartial:
		return "partial"
	case TriageNoProgress:
		return "no_progress"
	case TriageNeedsAgent:
		return "needs_agent"
	default:
		return "unknown"
	}
}
