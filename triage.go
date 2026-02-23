package main

import (
	"fmt"
	"strings"
	"time"
)

// gatherTriageEvidence collects git and beads signals from the session.
func gatherTriageEvidence(taskID, taskTitle, preSHA string,
	sessionStart time.Time, result *SessionResult, maxTurns int) *TriageEvidence {

	ev := &TriageEvidence{
		TaskID:    taskID,
		TaskTitle: taskTitle,
		PreSHA:    preSHA,
		NumTurns:  result.NumTurns,
		MaxTurns:  maxTurns,
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

	// Task status from beads.
	if task, err := getTaskStatus(taskID); err == nil {
		ev.TaskStatus = task.Status
	}

	// Beads created during session.
	if beads, err := getBeadsCreatedAfter(sessionStart); err == nil {
		ev.BeadsCreated = beads
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
			Reason:  "task already closed (bd show confirms)",
		}
	}

	hasCommits := ev.CommitCount > 0
	hasBeads := len(ev.BeadsCreated) > 0
	turnRatio := float64(0)
	if ev.MaxTurns > 0 {
		turnRatio = float64(ev.NumTurns) / float64(ev.MaxTurns)
	}

	// No progress at all — nothing happened.
	if !hasCommits && !hasBeads && !ev.HasUncommitted {
		return &TriageResult{
			Outcome: TriageNoProgress,
			Reason:  "no commits, no beads created, no uncommitted changes",
		}
	}

	// Commits exist, >50% turns used → agent made progress but ran out of budget.
	if hasCommits && turnRatio > 0.5 {
		return &TriageResult{
			Outcome: TriagePartial,
			Reason:  fmt.Sprintf("commits exist (%d), used %.0f%% of turns", ev.CommitCount, turnRatio*100),
		}
	}

	// Commits exist, <50% turns, session had error → crashed mid-work.
	if hasCommits && turnRatio <= 0.5 && ev.HadError {
		return &TriageResult{
			Outcome: TriagePartial,
			Reason:  fmt.Sprintf("commits exist (%d) but session errored at %.0f%% turns", ev.CommitCount, turnRatio*100),
		}
	}

	// Beads created during session but no commits → planning happened, no code.
	if hasBeads && !hasCommits {
		return &TriageResult{
			Outcome: TriagePartial,
			Reason:  fmt.Sprintf("%d beads created but no commits", len(ev.BeadsCreated)),
		}
	}

	// Commits exist, low turn usage, no error → ambiguous. Needs agent to examine.
	if hasCommits && turnRatio <= 0.5 && !ev.HadError {
		return &TriageResult{
			Outcome: TriageNeedsAgent,
			Reason:  fmt.Sprintf("commits exist (%d), only %.0f%% turns used, no error — ambiguous", ev.CommitCount, turnRatio*100),
		}
	}

	// Fallback: uncommitted changes only.
	if ev.HasUncommitted && !hasCommits && !hasBeads {
		return &TriageResult{
			Outcome: TriagePartial,
			Reason:  "uncommitted changes present but no commits or beads",
		}
	}

	// Catch-all: if we get here, treat as partial.
	return &TriageResult{
		Outcome: TriagePartial,
		Reason:  "unhandled evidence combination — treating as partial",
	}
}

// buildTriageComment creates a markdown summary for attachment to the bead.
func buildTriageComment(ev *TriageEvidence, outcome *TriageResult) string {
	var b strings.Builder

	b.WriteString("## Post-Session Triage\n\n")
	b.WriteString(fmt.Sprintf("**Outcome:** %s\n", triageOutcomeName(outcome.Outcome)))
	b.WriteString(fmt.Sprintf("**Reason:** %s\n\n", outcome.Reason))

	b.WriteString("### Evidence\n\n")
	b.WriteString(fmt.Sprintf("- Turns used: %d/%d", ev.NumTurns, ev.MaxTurns))
	if ev.MaxTurns > 0 {
		b.WriteString(fmt.Sprintf(" (%.0f%%)", float64(ev.NumTurns)/float64(ev.MaxTurns)*100))
	}
	b.WriteString("\n")

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

	if len(ev.BeadsCreated) > 0 {
		b.WriteString(fmt.Sprintf("- Beads created: %d\n", len(ev.BeadsCreated)))
		for _, bead := range ev.BeadsCreated {
			b.WriteString(fmt.Sprintf("  - %s: %s\n", bead.ID, bead.Title))
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
	b.WriteString(fmt.Sprintf("- Turns used: %d/%d", ev.NumTurns, ev.MaxTurns))
	if ev.MaxTurns > 0 {
		b.WriteString(fmt.Sprintf(" (%.0f%%)", float64(ev.NumTurns)/float64(ev.MaxTurns)*100))
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("- Task status (bd show): %s\n", ev.TaskStatus))
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
	if len(ev.BeadsCreated) > 0 {
		b.WriteString(fmt.Sprintf("- Beads created during session: %d\n", len(ev.BeadsCreated)))
		for _, bead := range ev.BeadsCreated {
			b.WriteString(fmt.Sprintf("  - %s: %s\n", bead.ID, bead.Title))
		}
	}
	if ev.HadError {
		b.WriteString("- Session error: yes\n")
	}

	b.WriteString("\n## Your Task\n\n")
	b.WriteString("Examine the evidence above. You may also:\n")
	b.WriteString("- Run `bd show " + ev.TaskID + " --json` to check current task state\n")
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
		MaxTurns: 10,
		Yolo:     cfg.Yolo,
		LogDir:   cfg.LogDir,
	}

	fmt.Printf("\n%s─── Triage: %s ──────────────────────────────────────%s\n\n",
		cYellow, ev.TaskID, cReset)

	triageResult := runSession(triageCfg, log, triagePrompt, stdinCh)

	// Log triage session costs.
	if triageResult.InputTokens > 0 || triageResult.OutputTokens > 0 {
		log.Log("Triage usage: %s input + %s output tokens, $%.4f, %d turns",
			formatTokens(triageResult.InputTokens), formatTokens(triageResult.OutputTokens),
			triageResult.TotalCostUSD, triageResult.NumTurns)
	}

	// Helper to populate cost fields from the triage session.
	setCosts := func(r *TriageResult) {
		r.TotalCostUSD = triageResult.TotalCostUSD
		r.InputTokens = triageResult.InputTokens
		r.OutputTokens = triageResult.OutputTokens
		r.NumTurns = triageResult.NumTurns
	}

	// Parse TRIAGE_STATUS sentinel.
	triageStatus := parseSentinelJSON[TriageStatus](triageResult.RawOutput, "TRIAGE_STATUS:")
	if triageStatus != nil {
		outcome := TriagePartial // default
		switch triageStatus.Outcome {
		case "complete":
			// Triage agent thinks it's complete — verify via fresh bd show.
			// (The evidence snapshot may be stale if the agent ran bd close.)
			if task, err := getTaskStatus(ev.TaskID); err == nil && task.Status == "closed" {
				outcome = TriageComplete
			} else {
				// Agent says complete but task isn't closed. Treat as partial
				// and include the agent's assessment in the comment.
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
