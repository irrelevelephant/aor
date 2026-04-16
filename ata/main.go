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

	// If a default remote is configured, proxy the command to it.
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

// tryRemote proxies the command to the default remote if one is configured.
// Returns (exitCode, true) if handled remotely, (0, false) otherwise.
func tryRemote(subcmd string, args []string) (int, bool) {
	switch subcmd {
	case "serve", "snapshot", "restore":
		return 0, false
	}

	cfg, err := config.Load()
	if err != nil {
		return 0, false
	}
	remote := cfg.ResolveRemote()
	if remote == nil {
		return 0, false
	}

	c := client.New(remote.URL)

	execArgs := args

	// Resolve file-based flags client-side: read the local file and replace
	// with the inline equivalent so the remote server doesn't need the file.
	execArgs, err = resolveFileFlags(execArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1, true
	}

	execArgs = injectClaimClientContext(subcmd, execArgs)

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

// fileFlagToInline maps file-based flags to their inline equivalents.
// When proxying to a remote, the file is read locally and sent as the inline flag.
var fileFlagToInline = map[string]string{
	"--spec-file": "--spec",
	"--desc-file": "--description",
	"--set-file":  "--set",
}

// resolveFileFlags reads any file-based flags (--spec-file, --desc-file, --set-file)
// from the local filesystem and replaces them with their inline equivalents
// (--spec, --description, --set) so the remote server receives the content directly.
func resolveFileFlags(args []string) ([]string, error) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]

		// Handle --flag=value form.
		if parts := strings.SplitN(arg, "=", 2); len(parts) == 2 {
			if inlineFlag, ok := fileFlagToInline[parts[0]]; ok {
				content, err := os.ReadFile(parts[1])
				if err != nil {
					return nil, fmt.Errorf("read %s: %w", parts[0], err)
				}
				out = append(out, inlineFlag, string(content))
				continue
			}
		}

		// Handle --flag value form.
		if inlineFlag, ok := fileFlagToInline[arg]; ok {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%s requires a value", arg)
			}
			i++
			content, err := os.ReadFile(args[i])
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", arg, err)
			}
			out = append(out, inlineFlag, string(content))
			continue
		}

		out = append(out, arg)
	}
	return out, nil
}

// injectClaimClientContext adds --host and --pid defaults resolved on the
// client side for `claim`, so a remote server records the caller's identity
// instead of its own.
func injectClaimClientContext(subcmd string, args []string) []string {
	if subcmd != "claim" {
		return args
	}
	var prefix []string
	if !hasFlag(args, "host") {
		if h, err := os.Hostname(); err == nil && h != "" {
			prefix = append(prefix, "--host", h)
		}
	}
	if !hasFlag(args, "pid") {
		prefix = append(prefix, "--pid", fmt.Sprintf("%d", os.Getpid()))
	}
	if len(prefix) == 0 {
		return args
	}
	return append(prefix, args...)
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
  clean     Delete tasks (closed by default, or --all)
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
  snapshot  Export all tasks to a .tar.gz archive
  restore   Import tasks from a snapshot archive (replaces existing)
  serve     Start web UI server
  remote    Manage remote server connections

Flags:
  --json    Output JSON (available on most commands)
`)
}
