package main

import (
	"fmt"
	"os"
	"strings"

	"aor/ata/client"
	"aor/ata/cmd"
	"aor/ata/config"
	"aor/ata/db"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subcmd := os.Args[1]
	args := os.Args[2:]

	// Commands that don't need a DB connection.
	switch subcmd {
	case "help", "--help", "-h":
		printUsage()
		return
	case "remote":
		if err := cmd.Remote(args); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Check if this workspace is configured for a remote server.
	if code, ok := tryRemote(subcmd, args); ok {
		os.Exit(code)
	}

	dbPath, err := db.DefaultDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	d, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening database: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()

	if err := cmd.Dispatch(d, subcmd, args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// tryRemote checks if the current workspace is configured for a remote server.
// Returns (exitCode, true) if the command was handled remotely, (0, false) otherwise.
func tryRemote(subcmd string, args []string) (int, bool) {
	switch subcmd {
	case "serve", "snapshot", "restore":
		return 0, false
	}

	cfg, err := config.Load()
	if err != nil || (len(cfg.Remotes) == 0 && cfg.DefaultRemote == "") {
		return 0, false
	}

	workspace := resolveWorkspaceForRemote(args)
	remote := cfg.ResolveRemote(workspace)
	if remote == nil {
		return 0, false
	}

	c := client.New(remote.URL)

	execArgs := args
	if remote.Workspace != "" && !hasFlag(args, "workspace") {
		execArgs = append([]string{"--workspace", remote.Workspace}, args...)
	}

	stdout, stderr, exitCode, err := c.Exec(subcmd, execArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote error: %v\n", err)
		return 1, true
	}
	if len(stdout) > 0 {
		os.Stdout.Write(stdout)
	}
	if stderr != "" {
		fmt.Fprint(os.Stderr, stderr)
	}
	return exitCode, true
}

// resolveWorkspaceForRemote determines the workspace without opening the DB.
// Mirrors detectWorkspace() logic but skips the DB-dependent IsRegisteredWorkspace check.
func resolveWorkspaceForRemote(args []string) string {
	for i, a := range args {
		if a == "--workspace" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, "--workspace=") {
			return strings.TrimPrefix(a, "--workspace=")
		}
	}

	if ws := os.Getenv("ATA_WORKSPACE"); ws != "" {
		return ws
	}

	toplevel := cmd.GitToplevel()
	if toplevel == "" {
		cwd, _ := os.Getwd()
		return cwd
	}

	if main := cmd.GitMainWorktree(); main != "" && main != toplevel {
		return main
	}

	return toplevel
}

// hasFlag checks if a flag name appears in args.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--"+name || strings.HasPrefix(a, "--"+name+"=") {
			return true
		}
	}
	return false
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `ata — Agent TAsks

Usage:
  ata <command> [args]

Commands:
  init      Register current directory as a workspace
  uninit    Unregister a workspace
  clean     Delete tasks (all or closed-only)
  create    Create a new task
  list      List tasks
  show      Show task details
  edit      Edit task title, body, or spec
  close     Close a task
  reopen    Reopen a closed task
  ready     List queue tasks (ready to work)
  claim     Claim a task (set in_progress)
  unclaim   Unclaim a task (reset to queue)
  promote   Promote a task to an epic
  spec      View or set epic spec
  comment   Add a comment to a task
  dep       Manage task dependencies
  tag       Manage task tags
  reorder   Reorder a task (--position, --before, --after, --top, --bottom)
  move      Batch move tasks between statuses
  recover   Recover stuck in_progress tasks
  epic-close-eligible  List epics eligible for close (--close to actually close)
  snapshot  Export workspace to a .tar.gz archive
  restore   Import workspace from a snapshot archive
  serve     Start web UI server
  remote    Manage remote server connections

Flags:
  --json    Output JSON (available on most commands)
`)
}
