package cmd

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"aor/ata/db"
)

func outputJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// GitToplevel returns the git rev-parse --show-toplevel path, or "" if not in a git repo.
func GitToplevel() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// GitMainWorktree returns the main worktree path from `git worktree list --porcelain`.
func GitMainWorktree() string {
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

// envAtaWorkspace is the environment variable that overrides workspace detection.
// Set by aor on child Claude processes so tasks created from worktrees use the
// main repo's registered workspace.
const envAtaWorkspace = "ATA_WORKSPACE"

// detectWorkspace auto-detects the workspace path, resolving worktrees
// to their registered main repo when possible.
// If ATA_WORKSPACE is set, it is used directly.
func detectWorkspace(d *db.DB) string {
	if ws := os.Getenv(envAtaWorkspace); ws != "" {
		return ws
	}

	toplevel := GitToplevel()
	if toplevel == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return ""
		}
		return cwd
	}

	// If toplevel is a registered workspace, use it directly.
	if ok, _ := d.IsRegisteredWorkspace(toplevel); ok {
		return toplevel
	}

	// Try the main worktree (first entry in `git worktree list`).
	main := GitMainWorktree()
	if main != "" && main != toplevel {
		if ok, _ := d.IsRegisteredWorkspace(main); ok {
			return main
		}
	}

	// Fall back to git toplevel.
	return toplevel
}

// rawWorkingDir returns the raw git toplevel or cwd (before workspace resolution).
// Used for created_in to record where the task was actually created.
func rawWorkingDir() string {
	toplevel := GitToplevel()
	if toplevel != "" {
		return toplevel
	}
	cwd, _ := os.Getwd()
	return cwd
}

// flagWasSet returns true if a flag was explicitly provided on the command line.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// readFileString reads a file and returns its contents as a string.
func readFileString(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func exitUsage(msg string) error {
	return fmt.Errorf("%s\nRun 'ata <command> --help' for usage", msg)
}

// splitFlagsAndPositional separates flag arguments from positional arguments.
// flagsWithValue is a set of flag names (without --) that take a value argument.
func splitFlagsAndPositional(args []string, flagsWithValue map[string]bool) (flags, positional []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "--") || strings.HasPrefix(arg, "-") {
			name := strings.TrimLeft(arg, "-")
			// Handle --flag=value
			if idx := strings.Index(name, "="); idx >= 0 {
				flags = append(flags, arg)
				continue
			}
			flags = append(flags, arg)
			// If the flag takes a value, consume the next arg too.
			if flagsWithValue[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			positional = append(positional, arg)
		}
	}
	return
}
