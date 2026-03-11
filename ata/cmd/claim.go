package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
)

func Claim(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("claim", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata claim ID [--json]")
	}

	id := positional[0]
	task, err := d.ClaimTask(id)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(task)
	}

	fmt.Printf("claimed %s: %s\n", task.ID, task.Title)
	return nil
}
