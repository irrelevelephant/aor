package main

import (
	"fmt"
	"os"
	"strings"

	"aor/afl/client"
	"aor/afl/cmd"
	"aor/afl/config"
	"aor/afl/db"
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
	case "config":
		if err := cmd.Config(args); err != nil {
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
	cfg, err := config.Load()
	if err != nil || (len(cfg.Remotes) == 0 && cfg.DefaultRemote == "") {
		return 0, false
	}

	workspace := resolveWorkspaceForRemote(cfg, args)
	remote := cfg.ResolveRemote(workspace)
	if remote == nil {
		return 0, false
	}

	c := client.New(remote.URL)

	execArgs := args
	if remote.Workspace != "" && !hasFlag(execArgs, "workspace") && acceptsWorkspaceFlag(subcmd) {
		execArgs = append([]string{"--workspace", remote.Workspace}, execArgs...)
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
func resolveWorkspaceForRemote(cfg config.Config, args []string) string {
	for i, a := range args {
		if a == "--workspace" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, "--workspace=") {
			return strings.TrimPrefix(a, "--workspace=")
		}
	}

	if ws := os.Getenv("AFL_WORKSPACE"); ws != "" {
		return ws
	}

	toplevel := cmd.GitToplevel()
	dir := toplevel
	if dir == "" {
		cwd, _ := os.Getwd()
		dir = cwd
	}

	mainWT := ""
	if toplevel != "" {
		mainWT = cmd.GitMainWorktree()
	}

	if ws := cfg.ResolveWorkspaceDir(dir, mainWT); ws != "" {
		return ws
	}

	if toplevel == "" {
		return dir
	}
	if mainWT != "" && mainWT != toplevel {
		return mainWT
	}
	return toplevel
}

// acceptsWorkspaceFlag returns true for subcommands that define a --workspace flag.
func acceptsWorkspaceFlag(subcmd string) bool {
	switch subcmd {
	case "init", "domain", "flow", "path", "capture":
		return true
	}
	return false
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
	fmt.Fprintf(os.Stderr, `afl — Agent FLows

Usage:
  afl <command> [args]

Commands:
  init      Register current directory as a workspace
  uninit    Unregister a workspace
  domain    Manage domains (create, list, show, delete)
  flow      Manage flows (create, list, show, delete)
  path      Manage paths (create, list, delete)
  step      Manage steps (create, list, edit, delete)
  capture   Manage screenshots (upload, batch, status, get)
  remote    Manage remote server connections
  config    Manage workspace config (default workspace, directory mappings)

Flags:
  --json        Output JSON (available on most commands)
  --workspace   Override workspace for this command

Workspace resolution (highest to lowest priority):
  1. --workspace flag
  2. AFL_WORKSPACE environment variable
  3. Directory mapping in ~/.afl/config.json ("workspaces")
  4. Default workspace in ~/.afl/config.json ("default_workspace")
  5. Git repo auto-detection
`)
}
