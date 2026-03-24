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
	host := fs.String("host", "", "Hostname of the calling machine (default: local hostname)")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{"pid": true, "host": true})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata claim ID [--json] [--pid PID] [--host HOST]")
	}

	id := positional[0]
	claimPID := *pid
	if claimPID == 0 {
		claimPID = os.Getpid()
	}
	claimHost := *host
	if claimHost == "" {
		claimHost, _ = os.Hostname()
	}
	task, err := d.ClaimTask(id, claimPID, claimHost)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(task)
	}

	fmt.Printf("claimed %s: %s\n", task.ID, task.Title)
	return nil
}
