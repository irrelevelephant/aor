package main

import (
	"fmt"
	"strings"
)

const maxInlineDiffChars = 50000

// buildReviewPrompt constructs the prompt for a code review session.
func buildReviewPrompt(diff, base string, round int, priorBeads []ReviewBead) string {
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
- ` + "`bd add --title \"<title>\" --priority <1-5> --type <bug|style|perf|security|logic|error-handling> --json`" + `
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

// buildPostTaskReviewPrompt constructs the prompt for a post-task review sub-agent.
// It reviews the diff produced by a single completed task, using the 8-dimension
// review criteria adapted from the review-changes skill.
func buildPostTaskReviewPrompt(diff, taskID, taskTitle string) string {
	var b strings.Builder

	// Role.
	fmt.Fprintf(&b, "You are an independent code reviewer. A task agent just completed task %s (%s). "+
		"Review the changes it made with the thoroughness of an experienced senior engineer.\n\n", taskID, taskTitle)

	// Diff.
	if len(diff) > 0 && len(diff) <= maxInlineDiffChars {
		b.WriteString("Here is the diff of changes made by the task agent:\n\n```diff\n")
		b.WriteString(diff)
		if !strings.HasSuffix(diff, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
	} else if len(diff) > maxInlineDiffChars {
		fmt.Fprintf(&b, "The diff is too large to inline (%d chars). Run `git log --oneline -5` to see recent commits, then `git diff` to inspect the changes.\n\n", len(diff))
	}

	// 8-dimension review criteria.
	b.WriteString(`## Review Criteria

Analyze the changes across these dimensions:

### 1. Correctness
- Logic errors or bugs
- Edge cases not handled
- Off-by-one errors
- Null/undefined handling
- Race conditions or async issues
- Type mismatches or incorrect type assertions

### 2. Code Quality
- Readability and clarity
- Unnecessary complexity
- Code duplication that should be extracted
- Naming (variables, functions, types)
- Dead code or unused imports
- Consistent patterns with rest of codebase

### 3. Architectural Consistency
- Module boundary violations
- Proper separation of concerns
- Consistent with existing patterns in the codebase
- Dependencies flowing in the right direction
- Following established conventions (check CLAUDE.md)

### 4. Security Concerns
- Injection vulnerabilities (SQL, command, XSS)
- Sensitive data exposure in logs or errors
- Input validation at trust boundaries
- Secrets or credentials in code

### 5. Error Handling
- Appropriate error handling at boundaries
- Error messages that help debugging
- No swallowed errors hiding problems

### 6. Performance
- Obvious performance issues (N+1 queries, unnecessary loops)
- Memory leaks (unclosed resources, growing collections)

### 7. Testing Implications
- Are existing tests still valid?
- Does this change need new tests?
- Are there untested code paths?

### 8. Maintainability
- Will future developers understand this?
- Is the change self-documenting?
- Are there implicit dependencies that should be explicit?

## Actions

For each issue you find:

1. **Small/medium issues**: Fix the code directly. Make atomic commits with descriptive messages (NO bead IDs in commits — stealth mode). Then file and close a bead for tracking.
2. **Large issues or pre-existing issues** (not introduced in this diff): File a bead only. Do NOT attempt to fix these.

Use ` + "`bd`" + ` to file beads:
- ` + "`bd add --title \"<title>\" --priority <1-5> --type <bug|style|perf|security|logic|error-handling> --json`" + `
- After fixing, close with: ` + "`bd update <id> --close --reason \"<what you did>\" --json`" + `

Important: Do NOT commit or push .beads/ files (stealth mode).

## Severity self-assessment

After your review, assess the overall severity:
- **critical**: data loss, security vulnerability, crash in common path
- **moderate**: incorrect behavior in edge cases, missing validation
- **minor**: style issues, non-idiomatic code, minor inefficiency
- **trivial**: cosmetic only, no behavioral impact

## Output format

When you are done, output EXACTLY this line so the orchestrator can parse it:

REVIEW_STATUS:{"beads_filed": [{"id": "<id>", "title": "<title>", "priority": <1-5>, "type": "<type>"}], "fixes_applied": ["<description>"], "summary": "<one-line summary>", "severity": "<critical|moderate|minor|trivial>", "error": null}

If no issues were found:
REVIEW_STATUS:{"beads_filed": [], "fixes_applied": [], "summary": "No issues found", "severity": "trivial", "error": null}

Start your review now.
`)

	return b.String()
}
