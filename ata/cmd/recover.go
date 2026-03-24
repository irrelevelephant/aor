package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
)

func Recover(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("recover", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Filter by workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	ws := *workspace
	if ws != "" {
		if resolved, err := d.ResolveWorkspace(ws); err == nil {
			ws = resolved
		}
	}

	recovered, err := d.RecoverStuckTasks(ws)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(recovered)
	}

	if len(recovered) == 0 {
		fmt.Println("no stuck tasks found")
		return nil
	}

	for _, t := range recovered {
		fmt.Printf("recovered %s: %s (PID %d dead)\n", t.ID, t.Title, t.ClaimedPID)
	}
	fmt.Printf("recovered %d task(s)\n", len(recovered))
	return nil
}
