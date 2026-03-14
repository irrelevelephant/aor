package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const statusClosed = "closed"

// runPull is the entry point for the "pull" subcommand.
// It claims a task, optionally creates a worktree, and launches an interactive
// Claude Code session with a structured planning workflow.
func runPull(args []string) error {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	noWorktree := fs.Bool("no-worktree", false, "Don't create a git worktree (default: create one)")
	workspace := fs.String("workspace", "", "Workspace path (default: auto-detect from git)")
	noYolo := fs.Bool("no-yolo", false, "Require permission prompts (default: skip)")
	forceInterview := fs.Bool("interview", false, "Force full interview (skip depth prompt)")
	noInterview := fs.Bool("no-interview", false, "Skip interview entirely")

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

	if *forceInterview && *noInterview {
		return fmt.Errorf("--interview and --no-interview are mutually exclusive")
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

	// Determine interview depth.
	depth := depthSkip
	switch {
	case *forceInterview:
		depth = depthFull
	case *noInterview:
		depth = depthSkip
	default:
		recommended, reason := assessInterviewDepth(task, epicSpec)
		depth = promptInterviewDepth(task, recommended, reason)
	}

	// Build prompt and launch interactive Claude session.
	prompt := buildPullPrompt(task, worktreePath, epicSpec, depth)

	fmt.Println("\nLaunching interactive planning session...")
	runInteractiveClaude([]string{prompt}, !*noYolo, worktreePath)

	// Check what happened to the task.
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
	default:
		// Task still open — commit any uncommitted changes, then close it.
		checkDir := worktreePath
		if checkDir == "" {
			checkDir = detectWorkDir()
		}

		if hasUncommittedChangesIn(checkDir) {
			fmt.Printf("\n%sUncommitted changes detected — running commit sweep...%s\n", cYellow, cReset)
			commitPrompt := "There are uncommitted changes. " +
				"Run `git diff` to see what changed, then stage and commit the changes " +
				"with a clear, descriptive commit message. Do not push."
			runInteractiveClaude([]string{commitPrompt}, !*noYolo, checkDir)
		}

		if err := closeTask(task.ID, "done"); err != nil {
			return fmt.Errorf("close task: %w", err)
		}
		fmt.Printf("\nTask %s closed.\n", task.ID)
	}

	return nil
}

// assessInterviewDepth determines the recommended interview depth based on
// task content and available context.
func assessInterviewDepth(task *AtaTask, epicSpec string) (interviewDepth, string) {
	// Already has a spec — no interview needed.
	if task.Spec != "" {
		return depthSkip, "Task already has a spec"
	}

	body := strings.TrimSpace(task.Body)

	// Empty body cases.
	if body == "" {
		if epicSpec == "" {
			return depthFull, "Task has no description and no epic spec"
		}
		return depthLight, "Task has no description but epic provides context"
	}

	// Short body without detail keywords.
	if len(body) < 100 {
		bodyLower := strings.ToLower(body)
		hasDetailKeywords := strings.Contains(bodyLower, "acceptance criteria") ||
			strings.Contains(bodyLower, "requirements") ||
			strings.Contains(bodyLower, "should") ||
			strings.Contains(bodyLower, "must")
		if !hasDetailKeywords {
			return depthLight, "Task has minimal detail"
		}
	}

	return depthSkip, "Task has sufficient detail"
}

// promptInterviewDepth shows the user the recommended depth and lets them choose.
func promptInterviewDepth(task *AtaTask, recommended interviewDepth, reason string) interviewDepth {
	fmt.Printf("\n%s── Interview Depth ─────────────────────────%s\n", cCyan, cReset)
	fmt.Printf("Task: %s%s%s — %s\n", cBold, task.ID, cReset, task.Title)

	body := strings.TrimSpace(task.Body)
	if body == "" {
		fmt.Printf("Body: %s(empty)%s\n", cGray, cReset)
	} else {
		fmt.Printf("Body: %d chars\n", len(body))
	}

	fmt.Printf("Assessment: %s\n\n", reason)

	options := []struct {
		depth interviewDepth
		label string
		desc  string
	}{
		{depthFull, "Full interview", "Conversational deep-dive, produces a spec"},
		{depthLight, "Light interview", "2-3 focused questions, adds a comment"},
		{depthSkip, "Skip", "Go straight to planning"},
	}

	for i, opt := range options {
		marker := "  "
		if opt.depth == recommended {
			marker = fmt.Sprintf("%s→%s", cGreen, cReset)
		}
		fmt.Printf("%s %d) %s — %s\n", marker, i+1, opt.label, opt.desc)
	}

	fmt.Printf("\nChoose [1/2/3, Enter=recommended]: ")

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)

	switch line {
	case "":
		return recommended
	case "1":
		return depthFull
	case "2":
		return depthLight
	case "3":
		return depthSkip
	default:
		fmt.Printf("Unrecognized input, using recommended.\n")
		return recommended
	}
}
