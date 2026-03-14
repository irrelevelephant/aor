package main

import (
	"fmt"
	"strings"
)

// buildPullPrompt constructs the prompt for an interactive pull session.
// It includes the task details, epic spec if applicable, worktree context,
// and the multi-phase workflow instructions.
func buildPullPrompt(task *AtaTask, worktreePath, epicSpec string) string {
	bt := "`"
	var b strings.Builder

	b.WriteString(fmt.Sprintf("You are working on task %s: %s\n\n", task.ID, task.Title))

	if task.Body != "" {
		b.WriteString(fmt.Sprintf("Task details:\n%s\n\n", task.Body))
	}

	if epicSpec != "" {
		b.WriteString(fmt.Sprintf("This task belongs to epic %s. Epic spec:\n%s\n\n", task.EpicID, epicSpec))
	}

	if worktreePath != "" {
		b.WriteString(fmt.Sprintf("You are working in a git worktree at: %s\n", worktreePath))
		b.WriteString("All changes should be made in this worktree.\n\n")
	}

	b.WriteString(`Follow this workflow:

## Phase 1: Research and Plan

Research the codebase to understand the current state and what needs to change.
Then write a clear, concrete plan with specific files and changes.

## Phase 2: Review with User

Once your plan is ready, present it to the user.
Then use the AskUserQuestion tool to ask them how to proceed.
IMPORTANT: You MUST use the AskUserQuestion tool (not just print text) so the user gets a native input prompt.
Ask: "How would you like to proceed? (execute / decompose / or describe changes to the plan)"

Based on their response:
- If they say "execute" or similar: go to Phase 3a
- If they say "decompose" or similar: go to Phase 3b
- Otherwise: treat their response as feedback on the plan, revise it, and ask again

## Phase 3a: Execute Directly

Implement the plan:
- Make all necessary code changes
- Run tests to verify your changes work
- Commit your changes with clear commit messages
- Run /simplify to review your changes for code quality, reuse, and efficiency
`)

	b.WriteString("- When done, exit the session (type /exit or Ctrl+D). The orchestrator will close the task.\n\n")

	b.WriteString("## Phase 3b: Decompose into Subtasks\n\n")
	b.WriteString("Break the work into subtasks:\n")
	b.WriteString(fmt.Sprintf("- Create each subtask: %sata create \"subtask title\" --body \"description\" --epic %s%s\n", bt, task.ID, bt))
	b.WriteString(fmt.Sprintf("- Add dependencies between subtasks where needed: %sata dep add TASK DEPENDS_ON%s\n", bt, bt))
	b.WriteString(fmt.Sprintf("- Order them by priority: %sata reorder ID --position N%s\n", bt, bt))
	b.WriteString("- Show the user the final breakdown\n")
	b.WriteString("- Use AskUserQuestion to confirm: \"Does this breakdown look good? (yes / or describe changes)\"\n")
	b.WriteString("- If they request changes, revise and ask again\n")
	b.WriteString("- Once confirmed, you're finished (the user will run aor to execute the subtasks autonomously)")

	return b.String()
}
