package main

import (
	"fmt"
	"os"

	"aor/ata/cmd"
	"aor/ata/db"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
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

	subcmd := os.Args[1]
	args := os.Args[2:]

	var cmdErr error
	switch subcmd {
	case "init":
		cmdErr = cmd.Init(d, args)
	case "clean":
		cmdErr = cmd.Clean(d, args)
	case "uninit":
		cmdErr = cmd.Uninit(d, args)
	case "create":
		cmdErr = cmd.Create(d, args)
	case "list":
		cmdErr = cmd.List(d, args)
	case "show":
		cmdErr = cmd.Show(d, args)
	case "edit":
		cmdErr = cmd.Edit(d, args)
	case "close":
		cmdErr = cmd.Close(d, args)
	case "ready":
		cmdErr = cmd.Ready(d, args)
	case "claim":
		cmdErr = cmd.Claim(d, args)
	case "unclaim":
		cmdErr = cmd.Unclaim(d, args)
	case "promote":
		cmdErr = cmd.Promote(d, args)
	case "spec":
		cmdErr = cmd.Spec(d, args)
	case "comment":
		cmdErr = cmd.Comment(d, args)
	case "dep":
		cmdErr = cmd.Dep(d, args)
	case "tag":
		cmdErr = cmd.Tag(d, args)
	case "reorder":
		cmdErr = cmd.Reorder(d, args)
	case "move":
		cmdErr = cmd.Move(d, args)
	case "recover":
		cmdErr = cmd.Recover(d, args)
	case "epic-close-eligible":
		cmdErr = cmd.EpicCloseEligible(d, args)
	case "snapshot":
		cmdErr = cmd.Snapshot(d, args)
	case "restore":
		cmdErr = cmd.Restore(d, args)
	case "serve":
		cmdErr = cmd.Serve(d, args)
	case "help", "--help", "-h":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", subcmd)
		printUsage()
		os.Exit(1)
	}

	if cmdErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", cmdErr)
		os.Exit(1)
	}
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
  ready     List queue tasks (ready to work)
  claim     Claim a task (set in_progress)
  unclaim   Unclaim a task (reset to queue)
  promote   Promote a task to an epic
  spec      View or set epic spec
  comment   Add a comment to a task
  dep       Manage task dependencies
  tag       Manage task tags
  reorder   Set task position in its list
  move      Batch move tasks between statuses
  recover   Recover stuck in_progress tasks
  epic-close-eligible  List epics eligible for auto-close
  snapshot  Export workspace to a .tar.gz archive
  restore   Import workspace from a snapshot archive
  serve     Start web UI server

Flags:
  --json    Output JSON (available on most commands)
`)
}
