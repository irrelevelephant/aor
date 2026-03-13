package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// runMerge is the entry point for the "merge" subcommand.
// It discovers worktrees, launches a single interactive Claude Code session
// to merge them into the main branch, and cleans up successfully merged worktrees.
func runMerge(args []string) error {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	exclude := fs.String("exclude", "", "Comma-separated worktree names to skip")
	noYolo := fs.Bool("no-yolo", false, "Require permission prompts (default: skip)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `aor merge — Merge worktrees back into the main branch

Usage:
  aor merge [flags] [WORKTREE_NAME...]

Discovers all git worktrees and merges them into the main branch using an
interactive Claude Code session. If specific worktree names are given, only
those are merged. Use --exclude to skip specific worktrees.

Worktree names are the directory basename (e.g., "myproject-f7q" for a
worktree at ../myproject-f7q).

Successfully merged worktrees are cleaned up (worktree removed, branch deleted).

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

	// Discover worktrees.
	worktrees, err := listWorktrees()
	if err != nil {
		return err
	}

	// Find the main worktree.
	var mainWT GitWorktree
	var candidates []GitWorktree
	for _, wt := range worktrees {
		if wt.IsMain {
			mainWT = wt
		} else {
			candidates = append(candidates, wt)
		}
	}

	if mainWT.Path == "" {
		return fmt.Errorf("could not determine main worktree")
	}

	if len(candidates) == 0 {
		fmt.Println("No worktrees to merge.")
		return nil
	}

	// Apply inclusion filter (positional args are worktree names).
	includeNames := fs.Args()
	if len(includeNames) > 0 {
		nameSet := make(map[string]bool)
		for _, n := range includeNames {
			nameSet[n] = true
		}
		var filtered []GitWorktree
		for _, wt := range candidates {
			if nameSet[filepath.Base(wt.Path)] {
				filtered = append(filtered, wt)
			}
		}
		candidates = filtered
	}

	// Apply exclusion filter.
	if *exclude != "" {
		excludeSet := make(map[string]bool)
		for _, n := range strings.Split(*exclude, ",") {
			excludeSet[strings.TrimSpace(n)] = true
		}
		var filtered []GitWorktree
		for _, wt := range candidates {
			if !excludeSet[filepath.Base(wt.Path)] {
				filtered = append(filtered, wt)
			}
		}
		candidates = filtered
	}

	if len(candidates) == 0 {
		fmt.Println("No worktrees to merge after filtering.")
		return nil
	}

	// Gather commit info for each worktree to include in the prompt.
	var infos []mergeWorktreeInfo
	for _, wt := range candidates {
		commits, _ := commitsBetween(mainWT.Branch, wt.Branch)
		infos = append(infos, mergeWorktreeInfo{
			GitWorktree: wt,
			Commits:     commits,
		})
	}

	// Print summary.
	fmt.Printf("Merging %d worktree(s) into %s:\n", len(infos), mainWT.Path)
	for _, info := range infos {
		commitCount := 0
		if info.Commits != "" {
			commitCount = strings.Count(info.Commits, "\n") + 1
		}
		fmt.Printf("  %s  (%s, %d commits)\n", filepath.Base(info.Path), info.Branch, commitCount)
	}
	fmt.Println()

	// Build prompt and launch interactive Claude session.
	prompt := buildMergePrompt(infos, mainWT)

	fmt.Println("Launching interactive merge session...")
	runInteractiveClaude([]string{prompt}, !*noYolo, mainWT.Path)

	return nil
}
