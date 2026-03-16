package main

import (
	"fmt"
	"strings"
)

// interviewDepth controls how much clarification happens before planning.
type interviewDepth int

const (
	depthFull  interviewDepth = iota + 1
	depthLight
	depthSkip
)

// lockedDecisionsWarning returns an IMPORTANT notice if the spec text contains
// a Locked Decisions section, or "" if it does not.
// label should describe the spec source, e.g. "epic spec" or "task spec".
func lockedDecisionsWarning(spec, label string) string {
	if !strings.Contains(spec, "## Locked Decisions") {
		return ""
	}
	return fmt.Sprintf(
		"IMPORTANT: The %s contains a 'Locked Decisions' section. "+
			"These are non-negotiable constraints. Do not propose alternatives or deviate from them.\n\n",
		label,
	)
}

// buildPullPrompt constructs the prompt for an interactive pull session.
// It includes the task details, epic spec if applicable, worktree context,
// and the multi-phase workflow instructions.
func buildPullPrompt(task *AtaTask, worktreePath, epicSpec string, depth interviewDepth) string {
	bt := "`"
	var b strings.Builder

	b.WriteString(fmt.Sprintf("You are working on task %s: %s\n\n", task.ID, task.Title))

	if task.Body != "" {
		b.WriteString(fmt.Sprintf("Task details:\n%s\n\n", task.Body))
	}

	if epicSpec != "" {
		b.WriteString(fmt.Sprintf("This task belongs to epic %s. Epic spec:\n%s\n\n", task.EpicID, epicSpec))
		b.WriteString(lockedDecisionsWarning(epicSpec, "epic spec"))
	}

	if task.Spec != "" {
		b.WriteString(fmt.Sprintf("Task spec:\n%s\n\n", task.Spec))
		b.WriteString(lockedDecisionsWarning(task.Spec, "task spec"))
	}

	if att := formatAttachments(task.Attachments, task.ID); att != "" {
		b.WriteString(att)
		b.WriteString("\n")
	}

	if worktreePath != "" {
		b.WriteString(fmt.Sprintf("You are working in a git worktree at: %s\n", worktreePath))
		b.WriteString("All changes should be made in this worktree.\n\n")
	}

	// Phase 0: Interview (conditional on depth).
	switch depth {
	case depthFull:
		b.WriteString(fmt.Sprintf(`## Phase 0: Deep Interview

Before planning, you need to understand what the user actually wants. This task has minimal detail, so conduct a conversational interview to clarify scope, requirements, and constraints.

**How to interview:**
- Start open-ended: "Tell me what you're trying to accomplish with this task."
- Follow the user's energy — dig deeper where they show uncertainty or excitement
- Challenge vague statements: "When you say 'better error handling', what does that look like concretely?"
- Ask for concrete examples and edge cases
- Cover these areas (naturally, not as a checklist): goal, scope boundaries, constraints, and done-criteria

**When you have enough clarity**, synthesize everything into a structured spec with these sections:
- **Goal**: What we're trying to achieve
- **Scope**: What's in and out
- **Approach**: High-level technical approach
- **Acceptance Criteria**: Concrete, testable conditions for "done"
- **Constraints**: Non-negotiable technical or process constraints
- **Locked Decisions**: Any decisions the user explicitly locked during the interview (these become non-negotiable during execution)

Write the spec to a temp file and save it:
%scat > /tmp/spec-%s.md << 'SPECEOF'
<your spec content>
SPECEOF
ata spec %s --set-file /tmp/spec-%s.md%s

Then proceed to Phase 1.

`, bt, task.ID, task.ID, task.ID, bt))

	case depthLight:
		b.WriteString(`## Phase 0: Quick Clarification

This task needs a bit more detail before planning. Ask the user 2-3 focused questions using AskUserQuestion:
- "What does 'done' look like for this task?"
- "What's the scope — what should this NOT touch?"
- "Any constraints or preferences I should know about?"

Don't ask all three if the first answer gives you enough context. Follow up naturally.

Once clarified, add the clarified scope as a comment:
` + bt + `ata comment ` + task.ID + ` "Clarified scope: <summary of what was discussed>" --author human` + bt + `

Then proceed to Phase 1.

`)
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

	b.WriteString("- When done, exit the session (/exit or Ctrl+D). The orchestrator will commit any remaining changes, close the task, and clean up.\n\n")

	b.WriteString("## Phase 3b: Decompose into Subtasks\n\n")
	b.WriteString("Break the work into subtasks:\n")
	b.WriteString(fmt.Sprintf("- Create each subtask: %s%s%s\n", bt, buildAtaCreateCmd("subtask title", ataCreateOpts{EpicID: task.ID, Body: "description", Workspace: task.Workspace}), bt))
	b.WriteString(fmt.Sprintf("- Add dependencies between subtasks where needed: %sata dep add TASK DEPENDS_ON%s\n", bt, bt))
	b.WriteString(fmt.Sprintf("- Order them by priority: %sata reorder ID --position N%s\n", bt, bt))
	b.WriteString("- Show the user the final breakdown\n")
	b.WriteString("- Use AskUserQuestion to confirm: \"Does this breakdown look good? (yes / or describe changes)\"\n")
	b.WriteString("- If they request changes, revise and ask again\n")
	b.WriteString("- Once confirmed, you're finished (the user will run aor to execute the subtasks autonomously)")

	return b.String()
}
