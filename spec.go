package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
)

// runSpec is the entry point for the "spec" subcommand.
// It reads one or more markdown spec files, launches an interactive Claude
// session to research, refine, and decompose them into epics/tasks, then
// optionally starts the orchestration loop to execute them.
func runSpec(args []string) error {
	fs := flag.NewFlagSet("spec", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace path (default: auto-detect from git)")
	noYolo := fs.Bool("no-yolo", false, "Require permission prompts (default: skip)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `aor spec — Spec-driven task planning and execution

Reads markdown spec files, refines them into proper specs, and decomposes
them into epics and tasks with dependencies. Launches an interactive session
with three phases: research, refine spec, propose tasks.

Usage:
  aor spec [flags] <file1.md> [file2.md ...]

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

	if fs.NArg() == 0 {
		fs.Usage()
		return fmt.Errorf("at least one spec file is required")
	}

	// Resolve workspace.
	ws := *workspace
	if ws == "" {
		ws = detectWorkspaceFromGit()
	}
	workDir := detectWorkDir()

	// Verify prerequisites.
	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("claude not found in PATH")
	}
	if err := findAta(); err != nil {
		return err
	}

	// Read spec files and build prompt contents in one pass.
	var specPaths []string
	var specContents []string
	for _, path := range fs.Args() {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read spec file %s: %w", path, err)
		}
		specPaths = append(specPaths, path)
		specContents = append(specContents, fmt.Sprintf("## File: %s\n\n%s", path, string(data)))
	}

	// Display what we're working with.
	fmt.Printf("%sSpec-driven planning%s\n", cBold, cReset)
	for _, p := range specPaths {
		fmt.Printf("  %s%s%s\n", cCyan, p, cReset)
	}
	fmt.Printf("  Workspace: %s\n", ws)
	fmt.Println()

	// Build prompt and launch interactive Claude session.
	prompt := buildSpecPrompt(specContents, ws)

	fmt.Println("Launching interactive planning session...")
	fmt.Printf("%s(Three phases: Research → Refine Spec → Propose Tasks)%s\n\n", cGray, cReset)

	yolo := !*noYolo
	runInteractiveClaude([]string{prompt}, yolo, workDir, ws)

	// After session ends, check if any epics were created and offer to execute.
	fmt.Println()
	offerExecution(ws, yolo, workDir)

	return nil
}

// offerExecution checks for queued epics and offers to start the orchestration loop.
func offerExecution(workspace string, yolo bool, workDir string) {
	// Check for ready tasks — if the session created epics/tasks, they'll be here.
	tasks, err := getReadyTasks("", "", workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sFailed to check ready tasks: %v%s\n", cRed, err, cReset)
		return
	}
	if len(tasks) == 0 {
		return
	}

	// Find epics among the tasks or their parents.
	epicIDs := map[string]bool{}
	for _, t := range tasks {
		if t.EpicID != "" {
			epicIDs[t.EpicID] = true
		}
	}

	if len(epicIDs) == 0 {
		fmt.Printf("%s%d ready task(s) found.%s\n", cGreen, len(tasks), cReset)
	} else {
		fmt.Printf("%s%d ready task(s) across %d epic(s) found.%s\n",
			cGreen, len(tasks), len(epicIDs), cReset)
	}

	fmt.Printf("\n%sStart executing now? [Y/n] %s", cYellow, cReset)

	stdinCh := startStdinReader()
	answer := <-stdinCh
	if answer == "n" || answer == "no" {
		fmt.Println("Tasks filed. Run `aor` to execute when ready.")
		return
	}

	cfg := &Config{
		Workspace: workspace,
		Yolo:      yolo,
		BatchSize: 1,
		LogDir:    resolveLogDir(),
		WorkDir:   workDir,
		StdinCh:   stdinCh,
	}

	// If there's exactly one epic, filter to it.
	if len(epicIDs) == 1 {
		for id := range epicIDs {
			cfg.EpicFilter = id
			fmt.Printf("\nRunning orchestration for epic %s...\n", id)
		}
	} else {
		fmt.Println("\nRunning orchestration loop...")
	}

	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "%serror: %v%s\n", cRed, err, cReset)
	}
}
