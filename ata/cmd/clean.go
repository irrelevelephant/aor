package cmd

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"aor/ata/db"
)

func Clean(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace name or path (default: auto-detect)")
	force := fs.Bool("force", false, "Skip confirmation prompt")

	if err := fs.Parse(args); err != nil {
		return err
	}

	ws := *workspace
	if ws == "" {
		ws = detectWorkspace(d)
	} else {
		// Resolve name to path if needed.
		if resolved, err := d.ResolveWorkspace(ws); err == nil {
			ws = resolved
		}
	}

	if !*force {
		fmt.Printf("This will delete ALL tasks and comments for workspace: %s\n", ws)
		fmt.Print("Continue? [y/N] ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(answer)) != "y" {
			fmt.Println("aborted")
			return nil
		}
	}

	deleted, err := d.CleanWorkspace(ws)
	if err != nil {
		return err
	}

	fmt.Printf("deleted %d tasks, unregistered workspace: %s\n", deleted, ws)
	return nil
}
