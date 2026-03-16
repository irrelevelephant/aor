package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
)

func Reopen(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("reopen", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata reopen ID")
	}

	id := positional[0]

	task, err := d.ReopenTask(id)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(task)
	}

	fmt.Printf("reopened %s: %s\n", task.ID, task.Title)
	return nil
}
