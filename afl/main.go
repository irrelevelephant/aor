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

	var (
		stdout                []byte
		stderr                string
		exitCode              int
		execErr               error
	)
	if up := detectUpload(subcmd, args); up != nil {
		stdout, stderr, exitCode, execErr = c.ExecWithUpload(subcmd, args, *up)
	} else {
		stdout, stderr, exitCode, execErr = c.Exec(subcmd, args)
	}
	if execErr != nil {
		fmt.Fprintf(os.Stderr, "remote error: %v\n", execErr)
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

// detectUpload returns the upload spec for the single known file/dir arg of
// the given command, or nil if the command has no streamable path. Commands
// with a file/dir arg:
//
//   - flow import <path>                                    → file
//   - capture upload <flow> <step> <platform> <image-path>  → file
//   - capture batch  <flow> <platform> <dir>                → dir
func detectUpload(subcmd string, args []string) *client.Upload {
	if len(args) < 2 {
		return nil
	}
	var (
		nth            int
		isDir          bool
		flagsWithValue map[string]bool
	)
	switch {
	case subcmd == "flow" && args[0] == "import":
		nth = 1
	case subcmd == "capture" && args[0] == "upload":
		nth = 4
		flagsWithValue = cmd.CaptureUploadFlags
	case subcmd == "capture" && args[0] == "batch":
		nth = 3
		isDir = true
		flagsWithValue = cmd.CaptureUploadFlags
	default:
		return nil
	}
	_, _, positionalIdx := cmd.SplitArgs(args, flagsWithValue)
	if nth >= len(positionalIdx) {
		return nil
	}
	return &client.Upload{ArgIdx: positionalIdx[nth], IsDir: isDir}
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
