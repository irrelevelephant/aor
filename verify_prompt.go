package main

import (
	"fmt"
	"strings"
)

// buildEpicVerifyPrompt constructs the prompt for the epic verification agent.
// The agent reads the epic spec, examines the codebase, and verifies acceptance criteria.
func buildEpicVerifyPrompt(epic *AtaTask, children []AtaTask, cfg *Config) string {
	var b strings.Builder

	b.WriteString("You are an epic verification agent. Your job is to verify that all acceptance criteria from the epic spec have been met by the completed work.\n\n")

	// Epic spec context (full ancestor chain — includes the epic itself).
	ancestors := getEpicAncestorSpecs(epic.ID)
	if len(ancestors) > 0 {
		b.WriteString("## Epic Spec Context\n\n")
		b.WriteString(formatAncestorSpecs(ancestors))
		b.WriteString("\n")
	}

	// Completed children with close reasons.
	b.WriteString("## Completed Tasks\n\n")
	for _, c := range children {
		reason := ""
		if c.CloseReason != "" {
			reason = fmt.Sprintf(" — %s", c.CloseReason)
		}
		fmt.Fprintf(&b, "- %s: %s [closed]%s\n", c.ID, c.Title, reason)
	}
	b.WriteString("\n")

	// Instructions.
	workspace := cfg.Workspace
	b.WriteString(`## Instructions

1. Read the epic spec above carefully. Identify every acceptance criterion, requirement, or deliverable.
2. For each criterion, examine the codebase to verify it has been implemented correctly:
   - Read the relevant source files
   - Run tests if applicable (go test, npm test, etc.)
   - Check that the implementation matches the spec requirements
3. After examining all criteria, output your verdict.

IMPORTANT:
- Do NOT modify any code. You are a verification agent — read-only.
- Do NOT close or modify any tasks.
- You MUST output the following sentinel as your final action.

`)

	// Filing instructions for failed criteria.
	fmt.Fprintf(&b, "If any criteria FAIL, file a new task for each gap using:\nata create \"<descriptive title>\" --status queue --epic \"%s\" --workspace \"%s\" --json\n\n", epic.ID, workspace)
	b.WriteString(sentinelBlock(
		"EPIC_VERIFY_STATUS",
		`{"passed": true, "tasks_filed": [], "summary": "<brief summary of verification>", "error": null}`,
		`{"passed": false, "tasks_filed": [], "summary": "", "error": "<description>"}`,
		"If criteria fail, include the tasks you filed in tasks_filed.",
	))
	b.WriteString("\n")

	return b.String()
}
