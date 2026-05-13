package cmd

import (
	"flag"
	"fmt"
	"slices"

	"aor/ata/db"
	"aor/ata/model"
)

func Unclaim(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("unclaim", flag.ContinueOnError)
	all := fs.Bool("all", false, "Unclaim every in_progress task")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	stdinIDs, err := readIDsFromStdin()
	if err != nil {
		return err
	}
	ids := append(slices.Clone(positional), stdinIDs...)

	if len(ids) > 0 {
		unclaimed, err := collectTasks(ids, func(id string) (*model.Task, error) {
			return d.UnclaimTask(id)
		})
		if err != nil {
			return err
		}
		return emitTasks("unclaimed", unclaimed, *jsonOut)
	}

	if !*all {
		return exitUsage("usage: ata unclaim ID [ID...]\n       <ID list> | ata unclaim\n       ata unclaim --all")
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
