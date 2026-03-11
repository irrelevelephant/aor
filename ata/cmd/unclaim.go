package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
)

func Unclaim(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("unclaim", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Unclaim all in_progress tasks for workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"workspace": true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	// If an ID is given, unclaim that specific task.
	if len(positional) > 0 {
		id := positional[0]
		task, err := d.UnclaimTask(id)
		if err != nil {
			return err
		}
		if *jsonOut {
			return outputJSON(task)
		}
		fmt.Printf("unclaimed %s: %s\n", task.ID, task.Title)
		return nil
	}

	// Otherwise, unclaim all in_progress for workspace.
	ws := *workspace
	if ws == "" {
		ws = detectWorkspace(d)
	}

	tasks, err := d.UnclaimByWorkspace(ws)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(tasks)
	}

	if len(tasks) == 0 {
		fmt.Println("no in-progress tasks found")
		return nil
	}

	for _, t := range tasks {
		fmt.Printf("unclaimed %s: %s\n", t.ID, t.Title)
	}
	return nil
}
