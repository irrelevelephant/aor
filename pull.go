package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	statusClosed     = "closed"
	statusInProgress = "in_progress"
)

// runPull is the entry point for the "pull" subcommand.
// It claims a task, optionally creates a worktree, and launches an interactive
// Claude Code session with a structured planning workflow.
func runPull(args []string) error {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	noWorktree := fs.Bool("no-worktree", false, "Don't create a git worktree (default: create one)")
	workspace := fs.String("workspace", "", "Workspace path (default: auto-detect from git)")
	noYolo := fs.Bool("no-yolo", false, "Require permission prompts (default: skip)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `aor pull — Interactive task planning and execution

Usage:
  aor pull [flags] [TASK_ID]

If no task ID is given, an interactive selector is shown.

Flags:
`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	// Resolve workspace.
	ws := *workspace
	if ws == "" {
		ws = detectWorkspaceFromGit()
	}

	// Verify prerequisites.
	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("claude not found in PATH")
	}
	if err := findAta(); err != nil {
		return err
	}

	// Get the task — either from argument or interactive selector.
	var task *AtaTask
	if fs.NArg() > 0 {
		id := fs.Arg(0)
		t, err := getTaskStatus(id)
		if err != nil {
			return err
		}
		task = t
	} else {
		tasks, err := getReadyTasks("", "", ws)
		if err != nil {
			return err
		}
		selected, err := selectTask(tasks)
		if err != nil {
			return err
		}
		if selected == nil {
			return nil // user cancelled
		}
		task = selected
	}

	if task.Status == statusClosed {
		return fmt.Errorf("task %s is already closed", task.ID)
	}

	// Create worktree if needed.
	var worktreePath string
	if !*noWorktree {
		wt, err := createWorktree(task.ID)
		if err != nil {
			return fmt.Errorf("worktree: %w", err)
		}
		worktreePath = wt
		fmt.Printf("Worktree: %s\n", worktreePath)
	}

	// Claim the task.
	if err := claimTask(task.ID); err != nil {
		return fmt.Errorf("claim: %w", err)
	}

	fmt.Printf("Pulled %s: %s\n", task.ID, task.Title)
	fmt.Printf("Status: in_progress\n")
	if task.Body != "" {
		fmt.Printf("\n%s\n", task.Body)
	}

	// Fetch epic spec once (used for display and prompt).
	var epicSpec string
	if task.EpicID != "" {
		epicSpec = getEpicSpec(task.EpicID)
		if epicSpec != "" {
			fmt.Printf("\n--- Epic Spec (%s) ---\n%s\n", task.EpicID, epicSpec)
		}
	}

	// Build prompt and launch interactive Claude session.
	prompt := buildPullPrompt(task, worktreePath, epicSpec)

	fmt.Println("\nLaunching interactive planning session...")
	runInteractiveClaude([]string{prompt}, !*noYolo, worktreePath)

	// Check what happened to the task and close it if work was done.
	task, err := getTaskStatus(task.ID)
	if err != nil {
		return err
	}

	switch {
	case task.Status == statusClosed:
		fmt.Printf("\nTask %s resolved.\n", task.ID)
	case task.IsEpic:
		fmt.Printf("\nTask %s promoted to epic.\n", task.ID)
		fmt.Printf("Run `aor --epic %s` to orchestrate execution.\n", task.ID)
	case task.Status == statusInProgress:
		fmt.Printf("\n%sClose task %s — %s? [Y/n] %s", cYellow, task.ID, task.Title, cReset)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		answer := strings.TrimSpace(strings.ToLower(line))
		if answer == "n" || answer == "no" {
			fmt.Printf("Task %s left as in_progress.\n", task.ID)
		} else {
			if err := closeTask(task.ID, "done"); err != nil {
				return fmt.Errorf("close task: %w", err)
			}
			fmt.Printf("Task %s closed.\n", task.ID)
		}
	default:
		fmt.Printf("\nTask %s is still %s.\n", task.ID, task.Status)
	}

	return nil
}
