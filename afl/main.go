package main

import (
	"fmt"
	"os"

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
	cfg, err := config.Load()
	if err != nil {
		return 0, false
	}
	remote := cfg.ResolveRemote()
	if remote == nil {
		return 0, false
	}

	c := client.New(remote.URL)

	stdout, stderr, exitCode, err := c.Exec(subcmd, args)
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

func printUsage() {
	fmt.Fprintf(os.Stderr, `afl — Agent FLows

Usage:
  afl <command> [args]

Commands:
  domain    Manage domains (create, list, show, delete)
  flow      Manage flows (create, list, show, delete, import)
  path      Manage paths (create, list, delete)
  step      Manage steps (create, list, edit, delete)
  capture   Manage screenshots (upload, batch, status, get)
  remote    Manage remote server connections

Flags:
  --json    Output JSON (available on most commands)
`)
}
