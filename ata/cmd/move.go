package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
	"aor/ata/model"
)

func Move(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("move", flag.ContinueOnError)
	from := fs.String("from", "", "Source status (queue, backlog, in_progress)")
	to := fs.String("to", "", "Target status (queue, backlog)")
	workspace := fs.String("workspace", "", "Workspace path")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"from":      true,
		"to":        true,
		"workspace": true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if *to == "" {
		return exitUsage("usage: ata move --from STATUS --to STATUS [--workspace WS]\n       ata move ID [ID...] --to STATUS")
	}

	// Validate target status.
	switch *to {
	case model.StatusQueue, model.StatusBacklog:
		// OK
	default:
		return fmt.Errorf("invalid target status %q (use queue or backlog)", *to)
	}

	// Validate source status if given.
	if *from != "" {
		switch *from {
		case model.StatusQueue, model.StatusBacklog, model.StatusInProgress:
			// OK
		default:
			return fmt.Errorf("invalid source status %q (use queue, backlog, or in_progress)", *from)
		}
	}

	if *from != "" && *from == *to {
		return fmt.Errorf("source and target status are the same (%s)", *from)
	}

	if len(positional) == 0 && *from == "" {
		return exitUsage("usage: ata move --from STATUS --to STATUS [--workspace WS]\n       ata move ID [ID...] --to STATUS")
	}

	ws := *workspace
	if ws == "" && len(positional) == 0 {
		ws = detectWorkspace(d)
	}

	tasks, err := d.MoveTasks(positional, *from, *to, ws)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(tasks)
	}

	if len(tasks) == 0 {
		fmt.Println("no tasks to move")
		return nil
	}

	for _, t := range tasks {
		fmt.Printf("moved %s → %s: %s\n", t.ID, *to, t.Title)
	}
	fmt.Printf("%d task(s) moved to %s\n", len(tasks), *to)
	return nil
}
