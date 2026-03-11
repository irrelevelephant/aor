package cmd

import (
	"flag"
	"fmt"
	"strings"

	"aor/ata/db"
)

func Close(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("close", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata close ID [REASON]")
	}

	id := positional[0]
	reason := strings.Join(positional[1:], " ")

	task, err := d.CloseTask(id, reason)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(task)
	}

	fmt.Printf("closed %s: %s\n", task.ID, task.Title)
	return nil
}
