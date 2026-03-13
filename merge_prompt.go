package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// mergeWorktreeInfo holds a worktree with additional context for the merge prompt.
type mergeWorktreeInfo struct {
	GitWorktree
	Commits string
}

// buildMergePrompt constructs the prompt for an interactive merge session.
// It includes worktree details with their commits and merge instructions.
func buildMergePrompt(worktrees []mergeWorktreeInfo, mainWT GitWorktree) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("You are merging git worktree branches back into the main branch (%s).\n", mainWT.Branch))
	b.WriteString(fmt.Sprintf("Main worktree: %s\n\n", mainWT.Path))
	b.WriteString("## Worktrees to merge\n\n")

	for _, wt := range worktrees {
		name := filepath.Base(wt.Path)
		b.WriteString(fmt.Sprintf("### %s\n", name))
		b.WriteString(fmt.Sprintf("- Path: %s\n", wt.Path))
		b.WriteString(fmt.Sprintf("- Branch: %s\n", wt.Branch))
		if wt.Commits != "" {
			b.WriteString(fmt.Sprintf("- Commits:\n```\n%s\n```\n", wt.Commits))
		} else {
			b.WriteString("- No unique commits\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(`## Instructions

You are working in the main worktree. Follow this process:

1. **Analyze the branches**: Look at each branch's commits and changes to understand what they do. Consider potential conflicts between branches.

2. **Decide merge order**: Choose an intelligent order that minimizes conflicts. Merge simpler/independent changes first.

3. **For each worktree**, merge its branch:
   - Run ` + "`git merge <branch>`" + ` (or ` + "`git rebase`" + ` if appropriate)
   - If there are merge conflicts:
     - Resolve them automatically using your understanding of the code
     - Only use AskUserQuestion if a conflict is genuinely ambiguous and you cannot determine the correct resolution
   - After a successful merge, clean up:
     - ` + "`git worktree remove <path>`" + `
     - ` + "`git branch -d <branch>`" + `

4. **Do NOT squash commits** — preserve the full commit history.

5. **Do NOT modify task status** — leave task management to the user.

6. After processing all worktrees, summarize:
   - Which branches were successfully merged
   - Which worktrees were cleaned up
   - Any issues encountered
`)

	return b.String()
}
