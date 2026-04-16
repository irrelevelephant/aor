package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
)

func Unclaim(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("unclaim", flag.ContinueOnError)
	all := fs.Bool("all", false, "Unclaim every in_progress task")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)

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

	if !*all {
		return exitUsage("usage: ata unclaim ID\n       ata unclaim --all")
	}

	tasks, err := d.UnclaimAll()
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
