package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
)

func Init(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace path (default: git toplevel or cwd)")
	name := fs.String("name", "", "Short name for the workspace")

	if err := fs.Parse(args); err != nil {
		return err
	}

	ws := *workspace
	if ws == "" {
		ws = gitToplevel()
		if ws == "" {
			ws = rawWorkingDir()
		}
	}

	if err := d.RegisterWorkspace(ws, *name); err != nil {
		return err
	}

	if *name != "" {
		fmt.Printf("registered workspace: %s (name: %s)\n", ws, *name)
	} else {
		fmt.Printf("registered workspace: %s\n", ws)
	}
	return nil
}
