package cmd

import (
	"flag"
	"fmt"
	"os"

	"aor/ata/db"
)

func Claim(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("claim", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")
	pid := fs.Int("pid", 0, "PID of the calling process (default: ata's own PID)")

	flagArgs, positional := splitFlagsAndPositional(args, nil)

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata claim ID [--json] [--pid PID]")
	}

	id := positional[0]
	claimPID := *pid
	if claimPID == 0 {
		claimPID = os.Getpid()
	}
	task, err := d.ClaimTask(id, claimPID)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(task)
	}

	fmt.Printf("claimed %s: %s\n", task.ID, task.Title)
	return nil
}
