package main

import (
	"fmt"
	"strings"
)

// buildSpecPrompt constructs the prompt for an interactive spec planning session.
// It includes the spec file contents, workspace context, and three-phase workflow.
func buildSpecPrompt(specContents []string, workspace string) string {
	bt := "`"
	var b strings.Builder

	b.WriteString("You are a technical architect planning implementation work from specification documents.\n\n")

	// Spec contents.
	b.WriteString("# Specification Documents\n\n")
	for _, spec := range specContents {
		b.WriteString(spec)
		if !strings.HasSuffix(spec, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n---\n\n")
	}

	// Workspace context.
	if workspace != "" {
		b.WriteString(fmt.Sprintf("Workspace: %s\n", workspace))
		b.WriteString(fmt.Sprintf("When creating tasks, use: %s%s%s\n\n", bt, buildAtaCreateCmd("title", ataCreateOpts{Workspace: workspace, JSON: true}), bt))
	}

	multiSpec := len(specContents) > 1

	// Workflow instructions.
	b.WriteString(`Follow this three-phase workflow. Each phase ends with a checkpoint where you ask the user for feedback before proceeding.

## Phase 1: Research

Research the codebase to understand:
- Current architecture and patterns
- Existing code that relates to the spec(s)
- Technical constraints and dependencies
- What already exists vs what needs to be built

After researching, present a brief summary of your findings to the user.
Then use the AskUserQuestion tool to ask:
"Research complete. Any areas I should investigate further, or proceed to spec refinement?"

Based on their response:
- If they give feedback: investigate further, update findings, and ask again
- If they say "proceed" or similar: move to Phase 2

## Phase 2: Refine Spec

`)

	if multiSpec {
		b.WriteString(`Take the original spec documents and refine them into proper, actionable specifications.
Analyze all specs together to identify:
- Cross-cutting concerns and shared components
- Potential conflicts or redundancies between specs
- Shared infrastructure or prerequisites

`)
	} else {
		b.WriteString("Take the original spec document and refine it into a proper, actionable specification.\n\n")
	}

	b.WriteString(`For each spec, ensure it has:
- Clear scope and boundaries (what's in and out)
- Specific acceptance criteria
- Technical approach informed by your codebase research
- Edge cases and error handling considerations
- Any assumptions called out explicitly

Present the refined spec(s) inline in full. This is the document that will become the epic spec.

Then use the AskUserQuestion tool to ask:
"Refined spec ready for review. Approve, or describe changes?"

Based on their response:
- If they give feedback: revise the spec and ask again
- If they approve: move to Phase 3

## Phase 3: Propose Execution Plan

Break the refined spec(s) into a concrete execution plan. Present a full plan document with these sections:

### Refined Specification
The final spec text (from Phase 2).

### Task Breakdown
For each task:
- **Title**: concise, action-oriented
- **Description**: what needs to be done, specific files/components involved
- **Dependencies**: which other tasks must complete first (by title reference)
- **Sort order**: suggested execution priority (lower = earlier)

`)

	if multiSpec {
		b.WriteString(`### Cross-Epic Dependencies
If tasks in one epic depend on tasks in another, call these out explicitly.

`)
	}

	b.WriteString(`### Dependency Graph
A text-based visualization of the task dependency graph.

### Implementation Notes
Any important technical decisions, risks, or sequencing rationale.

Present this full plan document inline.

Then use the AskUserQuestion tool to ask:
"Execution plan ready. Approve and create tasks, or describe changes?"

Based on their response:
- If they give feedback: revise the plan and ask again
- If they approve: proceed to create the epics and tasks

## Creating Epics and Tasks

Once the plan is approved:

`)

	epicCmd := buildAtaCreateCmd("Epic title", ataCreateOpts{Workspace: workspace, JSON: true})
	childCmd := buildAtaCreateCmd("Task title", ataCreateOpts{Body: "description", EpicID: "<epic-id>", Workspace: workspace, JSON: true})

	if multiSpec {
		fmt.Fprintf(&b, `1. For each spec, create an epic:
   %s%s%s
2. Promote each to an epic with the refined spec as the spec content:
   %sata promote <epic-id> --spec-file /dev/stdin%s (pipe the spec text)
   Note: since you can't pipe, instead use: write the spec to a temp file, then %sata promote <epic-id> --spec-file /tmp/spec-<id>.md%s
3. Create child tasks under each epic:
   %s%s%s
4. Set up dependencies between tasks:
   %sata dep add <task-id> <depends-on-id>%s
5. Set sort order for execution priority:
   %sata reorder <task-id> --position <N>%s
6. For cross-epic dependencies:
   %sata dep add <task-in-epic-B> <task-in-epic-A>%s

`, bt, epicCmd, bt, bt, bt, bt, bt, bt, childCmd, bt, bt, bt, bt, bt, bt, bt)
	} else {
		fmt.Fprintf(&b, `1. Create the epic:
   %s%s%s
2. Promote it with the refined spec:
   Write the refined spec to a temp file, then run: %sata promote <epic-id> --spec-file /tmp/spec-<id>.md%s
3. Create child tasks:
   %s%s%s
4. Set up dependencies:
   %sata dep add <task-id> <depends-on-id>%s
5. Set sort order:
   %sata reorder <task-id> --position <N>%s

`, bt, epicCmd, bt, bt, bt, bt, childCmd, bt, bt, bt, bt, bt)
	}

	b.WriteString(`After creating all epics, tasks, and dependencies, show a final summary:
- Number of epics created
- Number of tasks created
- Dependency graph with task IDs

Then use the AskUserQuestion tool to ask:
"All tasks filed. Ready to start execution, or review in the web UI first?"

You're done after this — the orchestrator will handle execution.
`)

	return b.String()
}
