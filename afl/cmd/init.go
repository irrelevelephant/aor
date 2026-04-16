package cmd

import (
	"flag"
	"fmt"
	"os"

	"aor/afl/db"
)

// Init registers a workspace.
func Init(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	name := fs.String("name", "", "Short name for the workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"name": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	var ws string
	if len(positional) > 0 {
		ws = positional[0]
	}
	if ws == "" {
		ws = GitToplevel()
		if ws == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working dir: %w", err)
			}
			ws = cwd
		}
	}

	if err := d.RegisterWorkspace(ws, *name); err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(map[string]any{
			"path": ws,
			"name": *name,
		})
	}

	if *name != "" {
		fmt.Printf("registered workspace: %s (name: %s)\n", ws, *name)
	} else {
		fmt.Printf("registered workspace: %s\n", ws)
	}
	return nil
}
