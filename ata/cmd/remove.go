package cmd

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"aor/ata/db"
)

func Remove(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	force := fs.Bool("force", false, "Skip confirmation prompts")
	clean := fs.Bool("clean", false, "Also delete all tasks")

	flagArgs, positional := splitFlagsAndPositional(args, nil)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	// Accept workspace as positional arg or auto-detect.
	var nameOrPath string
	if len(positional) > 0 {
		nameOrPath = positional[0]
	}

	// Resolve workspace. Use GetWorkspace to check registration and get
	// the name in one query (ResolveWorkspace already proved it exists).
	var ws string
	if nameOrPath == "" {
		ws = detectWorkspace(d)
	} else {
		resolved, err := d.ResolveWorkspace(nameOrPath)
		if err != nil {
			return err
		}
		ws = resolved
	}

	wsInfo, err := d.GetWorkspace(ws)
	if err != nil {
		return err
	}
	if wsInfo == nil {
		return fmt.Errorf("workspace %q is not registered", ws)
	}

	// Get task counts.
	open, closed, err := d.WorkspaceTaskCounts(ws)
	if err != nil {
		return err
	}
	total := open + closed

	displayName := ws
	if wsInfo.Name != "" {
		displayName = fmt.Sprintf("%s (%s)", wsInfo.Name, ws)
	}

	reader := bufio.NewReader(os.Stdin)

	// Confirm unregister.
	if !*force {
		fmt.Printf("Workspace: %s\n", displayName)
		fmt.Printf("  Open tasks:   %d\n", open)
		fmt.Printf("  Closed tasks: %d\n", closed)
		fmt.Printf("\nUnregister this workspace? [y/N] ")
		answer, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(answer)) != "y" {
			fmt.Println("aborted")
			return nil
		}
	}

	// Determine whether to also delete tasks.
	shouldClean := *clean
	if !shouldClean && !*force && total > 0 {
		fmt.Printf("Also delete all %d tasks? [y/N] ", total)
		answer, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(answer)) == "y" {
			shouldClean = true
		}
	}

	if shouldClean {
		deleted, removedDirs, err := doCleanWorkspace(d, ws)
		if err != nil {
			return err
		}

		fmt.Printf("unregistered workspace: %s\n", displayName)
		fmt.Printf("deleted %d tasks", deleted)
		if removedDirs > 0 {
			fmt.Printf(", removed %d attachment directories", removedDirs)
		}
		fmt.Println()
	} else {
		if err := d.UnregisterWorkspace(ws); err != nil {
			return err
		}
		fmt.Printf("unregistered workspace: %s\n", displayName)
		if total > 0 {
			fmt.Printf("%d tasks remain in database (%d open, %d closed)\n", total, open, closed)
		}
	}

	return nil
}
