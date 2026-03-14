package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// detectWorkDir returns the actual git toplevel for the current directory,
// WITHOUT resolving linked worktrees to the main worktree. This is the
// directory where agents should execute and make changes.
func detectWorkDir() string {
	toplevel, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		cwd, _ := os.Getwd()
		return cwd
	}
	return strings.TrimSpace(string(toplevel))
}

// resolveBase determines the base ref for diff comparison.
// If explicit is non-empty, it is used directly. Otherwise, auto-detection
// tries: git symbolic-ref refs/remotes/origin/HEAD, then main, then master.
func resolveBase(explicit string) (string, error) {
	if explicit != "" {
		// Verify the ref exists.
		if err := exec.Command("git", "rev-parse", "--verify", explicit).Run(); err != nil {
			return "", fmt.Errorf("ref %q not found: %w", explicit, err)
		}
		return explicit, nil
	}

	// Try symbolic-ref for origin's default branch.
	out, err := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD").Output()
	if err == nil {
		ref := strings.TrimSpace(string(out))
		// e.g. "refs/remotes/origin/main" → "origin/main"
		if strings.HasPrefix(ref, "refs/remotes/") {
			return strings.TrimPrefix(ref, "refs/remotes/"), nil
		}
		return ref, nil
	}

	// Fall back to checking main, then master.
	for _, branch := range []string{"main", "master"} {
		if exec.Command("git", "rev-parse", "--verify", "origin/"+branch).Run() == nil {
			return "origin/" + branch, nil
		}
		if exec.Command("git", "rev-parse", "--verify", branch).Run() == nil {
			return branch, nil
		}
	}

	return "", fmt.Errorf("cannot determine base branch: no origin/HEAD, main, or master found")
}

// diffRange returns the combined diff from base to HEAD plus any unstaged
// working tree changes. It concatenates `git diff <base>...HEAD` with
// `git diff HEAD` (working tree + staged).
func diffRange(base string) (string, error) {
	committed, err := exec.Command("git", "diff", base+"...HEAD").Output()
	if err != nil {
		// If three-dot fails (e.g. no merge base), fall back to two-dot.
		committed, err = exec.Command("git", "diff", base).Output()
		if err != nil {
			return "", fmt.Errorf("git diff %s: %w", base, err)
		}
	}

	working, err := exec.Command("git", "diff", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git diff HEAD: %w", err)
	}

	combined := string(committed)
	if len(working) > 0 {
		if len(combined) > 0 && !strings.HasSuffix(combined, "\n") {
			combined += "\n"
		}
		combined += string(working)
	}
	return combined, nil
}

// diffBetween returns the diff between two specific commit SHAs.
func diffBetween(fromSHA, toSHA string) (string, error) {
	out, err := exec.Command("git", "diff", fromSHA+".."+toSHA).Output()
	if err != nil {
		return "", fmt.Errorf("git diff %s..%s: %w", fromSHA, toSHA, err)
	}
	return string(out), nil
}

// detectWorktreeScope returns the worktree name if running inside a linked
// git worktree. Returns "" when in the main worktree or outside a git repo.
func detectWorktreeScope() string {
	gitDir, err := exec.Command("git", "rev-parse", "--git-dir").Output()
	if err != nil {
		return ""
	}
	commonDir, err := exec.Command("git", "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return ""
	}
	return worktreeName(strings.TrimSpace(string(gitDir)), strings.TrimSpace(string(commonDir)))
}

// worktreeName returns the worktree name given git-dir and git-common-dir paths.
// Returns "" when they resolve to the same directory (main worktree).
func worktreeName(gitDir, commonDir string) string {
	gd := filepath.Clean(gitDir)
	cd := filepath.Clean(commonDir)
	if gd == cd {
		return ""
	}
	// Linked worktree: git-dir is like <main>/.git/worktrees/<name>
	return filepath.Base(gd)
}

// gitMainWorktree returns the main worktree path from `git worktree list --porcelain`.
// The main worktree is always the first entry. Returns "" on error.
func gitMainWorktree() string {
	out, err := exec.Command("git", "worktree", "list", "--porcelain").Output()
	if err != nil {
		return ""
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "worktree ") {
			return strings.TrimPrefix(line, "worktree ")
		}
	}
	return ""
}

// listWorktrees returns all git worktrees by parsing `git worktree list --porcelain`.
// The first entry is always the main worktree.
func listWorktrees() ([]GitWorktree, error) {
	out, err := exec.Command("git", "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}

	var worktrees []GitWorktree
	var current GitWorktree
	first := true

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			if current.Path != "" {
				worktrees = append(worktrees, current)
			}
			current = GitWorktree{Path: strings.TrimPrefix(line, "worktree ")}
			if first {
				current.IsMain = true
				first = false
			}
		case strings.HasPrefix(line, "HEAD "):
			current.HEAD = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	return worktrees, nil
}

// createWorktree creates (or reuses) a git worktree for the given task ID.
// Returns the absolute path to the worktree directory.
func createWorktree(taskID string) (string, error) {
	mainWT := gitMainWorktree()
	if mainWT == "" {
		return "", fmt.Errorf("could not determine main worktree (not in a git repo?)")
	}

	repoBase := filepath.Base(mainWT)
	wtPath := filepath.Join(filepath.Dir(mainWT), repoBase+"-"+taskID)
	branch := "task/" + taskID

	// If the worktree directory already exists, reuse it.
	if info, err := os.Stat(wtPath); err == nil && info.IsDir() {
		return wtPath, nil
	}

	// Check if the branch already exists.
	branchExists := false
	bout, err := exec.Command("git", "branch", "--list", branch).Output()
	if err == nil && strings.TrimSpace(string(bout)) != "" {
		branchExists = true
	}

	var cmd *exec.Cmd
	if branchExists {
		cmd = exec.Command("git", "worktree", "add", wtPath, branch)
	} else {
		cmd = exec.Command("git", "worktree", "add", wtPath, "-b", branch)
	}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git worktree add: %w", err)
	}

	return wtPath, nil
}

// removeWorktree removes a git worktree and optionally deletes its branch.
func removeWorktree(wtPath string, deleteBranch bool) error {
	// Get branch name before removing.
	var branch string
	if deleteBranch {
		cmd := exec.Command("git", "-C", wtPath, "rev-parse", "--abbrev-ref", "HEAD")
		if out, err := cmd.Output(); err == nil {
			branch = strings.TrimSpace(string(out))
		}
	}

	cmd := exec.Command("git", "worktree", "remove", wtPath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git worktree remove %s: %w", wtPath, err)
	}

	if branch != "" && branch != "HEAD" {
		exec.Command("git", "branch", "-d", branch).Run()
	}
	return nil
}

// hasUncommittedChanges returns true if the working tree has uncommitted changes.
func hasUncommittedChanges() bool {
	return hasUncommittedChangesIn("")
}

// hasUncommittedChangesIn checks for uncommitted changes in a specific directory.
// If dir is empty, uses the current working directory.
func hasUncommittedChangesIn(dir string) bool {
	cmd := exec.Command("git", "status", "--porcelain")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// commitsBetween returns git log --oneline from..to.
func commitsBetween(from, to string) (string, error) {
	out, err := exec.Command("git", "log", "--oneline", from+".."+to).Output()
	if err != nil {
		return "", fmt.Errorf("git log %s..%s: %w", from, to, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// diffStatBetween returns git diff --stat from..to.
func diffStatBetween(from, to string) (string, error) {
	out, err := exec.Command("git", "diff", "--stat", from+".."+to).Output()
	if err != nil {
		return "", fmt.Errorf("git diff --stat %s..%s: %w", from, to, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// commitCountBetween returns the number of commits between two SHAs.
func commitCountBetween(from, to string) (int, error) {
	out, err := exec.Command("git", "rev-list", "--count", from+".."+to).Output()
	if err != nil {
		return 0, fmt.Errorf("git rev-list --count %s..%s: %w", from, to, err)
	}
	s := strings.TrimSpace(string(out))
	var count int
	if _, err := fmt.Sscanf(s, "%d", &count); err != nil {
		return 0, fmt.Errorf("parse count %q: %w", s, err)
	}
	return count, nil
}

// headSHA returns the current HEAD commit SHA.
func headSHA() (string, error) {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
