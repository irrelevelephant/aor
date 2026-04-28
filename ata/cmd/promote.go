package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
)

func Promote(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("promote", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata promote ID [--json]")
	}

	id := positional[0]
	task, err := d.PromoteToEpic(id)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(task)
	}

	fmt.Printf("promoted %s to epic: %s\n", task.ID, task.Title)
	return nil
}
