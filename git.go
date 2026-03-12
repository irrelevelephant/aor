package main

import (
	"bufio"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

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

// hasUncommittedChanges returns true if the working tree has uncommitted changes.
func hasUncommittedChanges() bool {
	out, err := exec.Command("git", "status", "--porcelain").Output()
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
