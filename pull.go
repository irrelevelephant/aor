package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

	// Fetch epic spec chain (used for display and prompt).
	var epicSpec string
	if task.EpicID != "" {
		ancestors := getEpicAncestorSpecs(task.EpicID)
		epicSpec = formatAncestorSpecs(ancestors)
		for _, a := range ancestors {
			fmt.Printf("\n--- Epic Spec (%s) ---\n%s\n", a.ID, a.Spec)
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
		var cancelled bool
		depth, cancelled = promptInterviewDepth(task, recommended, reason)
		if cancelled {
			return nil
		}
	}

	// Build prompt and launch interactive Claude session.
	prompt := buildPullPrompt(task, worktreePath, epicSpec, depth)

	fmt.Println("\nLaunching interactive planning session...")
	runInteractiveClaude([]string{prompt}, !*noYolo, worktreePath, ws)

	// Check what happened to the task.
	task, err := getTaskStatus(task.ID)
	if err != nil {
		return err
	}

	switch {
	case task.Status == statusClosed:
		fmt.Printf("\nTask %s resolved.\n", task.ID)
	case task.IsEpic:
		if err := unclaimTask(task.ID); err != nil {
			return fmt.Errorf("unclaim epic: %w", err)
		}
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
			commitPrompt := buildCommitSweepPrompt("", "a clear, descriptive commit message")
			runInteractiveClaude([]string{commitPrompt}, !*noYolo, checkDir, ws)
		}

		if err := closeTask(task.ID, "done"); err != nil {
			return fmt.Errorf("close task: %w", err)
		}
		fmt.Printf("\nTask %s closed.\n", task.ID)
	}

	// Clean up worktree: merge branch back into main and remove.
	if worktreePath != "" {
		fmt.Println()
		if strategy, err := mergeWorktreeBranch(worktreePath); err != nil {
			fmt.Printf("%sWorktree cleanup failed: %v%s\n", cYellow, err, cReset)
			fmt.Printf("Run 'aor merge' to clean up manually.\n")
		} else {
			fmt.Printf("Worktree integrated and cleaned up (%s).\n", strategy)
		}
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

// depthOption pairs an interview depth with display text.
type depthOption struct {
	depth interviewDepth
	label string
	desc  string
}

var depthOptions = []depthOption{
	{depthFull, "Full interview", "Conversational deep-dive, produces a spec"},
	{depthLight, "Light clarification", "2-3 focused questions, adds a comment"},
	{depthSkip, "Skip", "Go straight to planning"},
}

// Styles for the depth selector TUI.
var (
	depthTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	depthDimStyle   = lipgloss.NewStyle().Faint(true)
	depthBoldStyle  = lipgloss.NewStyle().Bold(true)
	depthRecStyle   = lipgloss.NewStyle().Faint(true).Italic(true)
)

// depthModel is a bubbletea model for selecting interview depth.
type depthModel struct {
	cursor      int
	recommended int
	subtitle    string // precomputed context line (body info + reason)
	chosen      interviewDepth
	cancelled   bool
}

func (m depthModel) Init() tea.Cmd { return nil }

func (m depthModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(depthOptions)-1 {
				m.cursor++
			}
		case "enter":
			m.chosen = depthOptions[m.cursor].depth
			return m, tea.Quit
		case "q", "esc", "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m depthModel) View() string {
	var b strings.Builder
	b.WriteString(depthTitleStyle.Render("Interview Depth"))
	b.WriteString("\n")
	b.WriteString(depthDimStyle.Render(m.subtitle))
	b.WriteString("\n\n")

	for i, opt := range depthOptions {
		cursor := "  "
		label := opt.label + "  " + depthDimStyle.Render(opt.desc)
		if i == m.cursor {
			cursor = "> "
			label = depthBoldStyle.Render(opt.label) + "  " + depthDimStyle.Render(opt.desc)
		}
		rec := ""
		if i == m.recommended {
			rec = "  " + depthRecStyle.Render("(recommended)")
		}
		b.WriteString(cursor + label + rec + "\n")
	}

	b.WriteString(depthDimStyle.Render("\n↑/↓ to move, enter to select, esc to cancel"))
	return b.String()
}

// promptInterviewDepth shows the user the recommended depth and lets them choose
// using an interactive arrow-key selector. Returns the chosen depth and false,
// or zero and true if the user cancelled.
func promptInterviewDepth(task *AtaTask, recommended interviewDepth, reason string) (interviewDepth, bool) {
	// Find index of recommended option.
	recIdx := 0
	for i, opt := range depthOptions {
		if opt.depth == recommended {
			recIdx = i
			break
		}
	}

	// Precompute subtitle from task body + reason.
	subtitle := "No task description"
	if body := strings.TrimSpace(task.Body); body != "" {
		subtitle = fmt.Sprintf("Task body: %d chars", len(body))
	}
	subtitle += " — " + reason

	m := depthModel{
		cursor:      recIdx,
		recommended: recIdx,
		subtitle:    subtitle,
	}

	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		return recommended, false
	}

	final := result.(depthModel)
	if final.cancelled {
		return 0, true
	}
	return final.chosen, false
}
