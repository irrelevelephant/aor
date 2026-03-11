package cmd

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	"aor/ata/db"
	"aor/ata/model"
)

func Pull(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() == 0 {
		return exitUsage("usage: ata pull ID")
	}

	id := fs.Arg(0)

	// Get the task.
	task, err := d.GetTask(id)
	if err != nil {
		return err
	}

	if task.Status == model.StatusClosed {
		return fmt.Errorf("task %s is already closed", id)
	}

	// Move to in_progress if needed.
	if task.Status != model.StatusInProgress {
		ws := rawWorkingDir()
		task, err = d.ForceClaimTask(id, ws)
		if err != nil {
			return fmt.Errorf("claim: %w", err)
		}
	}
	fmt.Printf("Pulled %s: %s\n", task.ID, task.Title)
	fmt.Printf("Status: %s\n", task.Status)
	if task.Body != "" {
		fmt.Printf("\n%s\n", task.Body)
	}

	// If the task is under an epic, show the spec.
	if task.EpicID != "" {
		epic, err := d.GetTask(task.EpicID)
		if err == nil && epic.Spec != "" {
			fmt.Printf("\n--- Epic Spec (%s: %s) ---\n%s\n", epic.ID, epic.Title, epic.Spec)
		}
	}

	planPrompt := buildPullPrompt(*task)

	claude, err := exec.LookPath("claude")
	if err != nil {
		fmt.Println("\nclaude not in PATH — skipping interactive planning")
		return nil
	}

	fmt.Println("\nLaunching interactive planning session...")
	cmd := exec.Command(claude, "--dangerously-skip-permissions", planPrompt)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Claude exited non-zero (user quit, ctrl-c, etc.) — don't treat as fatal.
		fmt.Printf("\nClaude session ended: %v\n", err)
	}

	// Check what happened to the task.
	task, err = d.GetTask(id)
	if err != nil {
		return err
	}

	switch {
	case task.Status == model.StatusClosed:
		fmt.Printf("\nTask %s resolved.\n", id)
	case task.IsEpic:
		children, _ := d.ListTasks("", "", id, "")
		fmt.Printf("\nTask %s promoted to epic with %d subtasks.\n", id, len(children))
		fmt.Printf("Run `aor --epic %s` to orchestrate execution.\n", id)
	default:
		fmt.Printf("\nTask %s is still %s.\n", id, task.Status)
	}

	return nil
}

func buildPullPrompt(task model.Task) string {
	bt := "`"
	return fmt.Sprintf("You are working on task %s: %s\n"+
		"\n"+
		"Task details:\n"+
		"%s\n"+
		"\n"+
		"Follow this workflow:\n"+
		"\n"+
		"## Phase 1: Research and Plan\n"+
		"\n"+
		"Research the codebase to understand the current state and what needs to change.\n"+
		"Then write a clear, concrete plan with specific files and changes.\n"+
		"\n"+
		"## Phase 2: Review with User\n"+
		"\n"+
		"Once your plan is ready, present it to the user.\n"+
		"Then use the AskUserQuestion tool to ask them how to proceed.\n"+
		"IMPORTANT: You MUST use the AskUserQuestion tool (not just print text) so the user gets a native input prompt.\n"+
		"Ask: \"How would you like to proceed? (execute / decompose / or describe changes to the plan)\"\n"+
		"\n"+
		"Based on their response:\n"+
		"- If they say \"execute\" or similar: go to Phase 3a\n"+
		"- If they say \"decompose\" or similar: go to Phase 3b\n"+
		"- Otherwise: treat their response as feedback on the plan, revise it, and ask again\n"+
		"\n"+
		"## Phase 3a: Execute Directly\n"+
		"\n"+
		"Implement the plan:\n"+
		"- Make all necessary code changes\n"+
		"- Run tests to verify your changes work\n"+
		"- Commit your changes with clear commit messages\n"+
		"- Run /simplify to review your changes for code quality, reuse, and efficiency\n"+
		"- When done, use AskUserQuestion to ask: \"Work complete. Resolve task %s and exit? (yes/no)\"\n"+
		"  - If yes: run "+bt+"ata close %s \"done\""+bt+" and you're finished\n"+
		"  - If no: use AskUserQuestion to ask what else they'd like to do\n"+
		"\n"+
		"## Phase 3b: Decompose into Subtasks\n"+
		"\n"+
		"Break the work into subtasks:\n"+
		"- Create each subtask: "+bt+"ata create \"subtask title\" --body \"description\" --epic %s"+bt+"\n"+
		"- Add dependencies between subtasks where needed: "+bt+"ata dep add TASK DEPENDS_ON"+bt+"\n"+
		"- Order them by priority: "+bt+"ata reorder ID --position N"+bt+"\n"+
		"- Show the user the final breakdown\n"+
		"- Use AskUserQuestion to confirm: \"Does this breakdown look good? (yes / or describe changes)\"\n"+
		"- If they request changes, revise and ask again\n"+
		"- Once confirmed, you're finished (the user will run aor to execute the subtasks autonomously)",
		task.ID, task.Title, task.Body,
		task.ID, task.ID,
		task.ID)
}
