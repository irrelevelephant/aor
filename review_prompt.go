package main

import (
	"fmt"
	"strings"
)

const maxInlineDiffChars = 50000

// bdAddCmd returns the bd add command template, including --labels if scope is set.
func bdAddCmd(scope string) string {
	if scope != "" {
		return fmt.Sprintf("`bd add --title \"<title>\" --priority <1-5> --type <bug|style|perf|security|logic|error-handling> --labels \"%s\" --json`", scope)
	}
	return "`bd add --title \"<title>\" --priority <1-5> --type <bug|style|perf|security|logic|error-handling> --json`"
}

// buildReviewPrompt constructs the prompt for a code review session.
func buildReviewPrompt(diff, base string, round int, priorBeads []ReviewBead, scope string) string {
	var b strings.Builder

	// Role.
	fmt.Fprintf(&b, "You are reviewing code changes from `%s` to the current state (HEAD + working tree).\n\n", base)

	// Diff.
	if len(diff) > 0 && len(diff) <= maxInlineDiffChars {
		b.WriteString("Here is the diff to review:\n\n```diff\n")
		b.WriteString(diff)
		if !strings.HasSuffix(diff, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
	} else if len(diff) > maxInlineDiffChars {
		fmt.Fprintf(&b, "The diff is too large to inline (%d chars). Run `git diff %s...HEAD` and `git diff HEAD` yourself to see the full changes.\n\n", len(diff), base)
	} else {
		b.WriteString("There is no diff — the working tree matches the base. If you believe there should be changes, verify the base ref.\n\n")
	}

	// Prior round context (capped to avoid context bloat).
	const maxPriorBeads = 30
	if round > 1 && len(priorBeads) > 0 {
		displayBeads := priorBeads
		truncated := 0
		if len(priorBeads) > maxPriorBeads {
			displayBeads = priorBeads[len(priorBeads)-maxPriorBeads:]
			truncated = len(priorBeads) - maxPriorBeads
		}
		b.WriteString("## Prior review rounds\n\n")
		b.WriteString("The following beads were filed in earlier rounds. Do NOT re-file these — they are already tracked:\n\n")
		if truncated > 0 {
			fmt.Fprintf(&b, "(%d older beads omitted)\n", truncated)
		}
		for _, bead := range displayBeads {
			fmt.Fprintf(&b, "- %s (P%d, %s): %s\n", bead.ID, bead.Priority, bead.Type, bead.Title)
		}
		b.WriteString("\n")
	}

	// Review instructions.
	b.WriteString(`## Instructions

For each issue you find:

1. **Small/medium issues in the current diff**: Fix the code directly. Make a commit referencing the bead ID. Then close the bead with a reason.
2. **Large issues or pre-existing issues** (not introduced in this diff): File a bead only. Do NOT attempt to fix these.

Use ` + "`bd`" + ` to file beads:
- ` + bdAddCmd(scope) + `
- After fixing, close with: ` + "`bd update <id> --close --reason \"<what you did>\" --json`" + `

## Review focus (in priority order)

1. Correctness / logic errors
2. Bugs (nil derefs, off-by-one, race conditions)
3. Security vulnerabilities
4. Missing error handling
5. Performance issues
6. Style / readability

## Severity self-assessment

After your review, assess the overall severity of issues found:
- **critical**: data loss, security vulnerability, crash in common path
- **moderate**: incorrect behavior in edge cases, missing validation
- **minor**: style issues, non-idiomatic code, minor inefficiency
- **trivial**: cosmetic only, no behavioral impact

`)

	// Sentinel instructions.
	b.WriteString(`## Output format

When you are done, output EXACTLY this line so the orchestrator can parse it:

REVIEW_STATUS:{"beads_filed": [{"id": "<id>", "title": "<title>", "priority": <1-5>, "type": "<type>"}], "fixes_applied": ["<description>"], "summary": "<one-line summary>", "severity": "<critical|moderate|minor|trivial>", "error": null}

If no issues were found:
REVIEW_STATUS:{"beads_filed": [], "fixes_applied": [], "summary": "No issues found", "severity": "trivial", "error": null}

If you encounter an error:
REVIEW_STATUS:{"beads_filed": [], "fixes_applied": [], "summary": "", "severity": "", "error": "<description>"}

Start your review now.
`)

	return b.String()
}

