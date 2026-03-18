package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
)

func Reorder(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("reorder", flag.ContinueOnError)
	position := fs.Int("position", -1, "Target position (0-based)")
	before := fs.String("before", "", "Place before this task ID")
	after := fs.String("after", "", "Place after this task ID")
	top := fs.Bool("top", false, "Move to top of list")
	bottom := fs.Bool("bottom", false, "Move to bottom of list")
	status := fs.String("status", "", "Move to a different status (top-level only)")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"position": true,
		"before":   true,
		"after":    true,
		"status":   true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata reorder ID [--position N | --before ID | --after ID | --top | --bottom] [--status STATUS]")
	}

	// Count how many position specifiers were given.
	specCount := 0
	if *position >= 0 {
		specCount++
	}
	if *before != "" {
		specCount++
	}
	if *after != "" {
		specCount++
	}
	if *top {
		specCount++
	}
	if *bottom {
		specCount++
	}
	if specCount == 0 {
		return exitUsage("usage: ata reorder ID [--position N | --before ID | --after ID | --top | --bottom] [--status STATUS]")
	}
	if specCount > 1 {
		return fmt.Errorf("specify exactly one of --position, --before, --after, --top, --bottom")
	}

	id := positional[0]

	// Look up the task to detect epic children.
	task, err := d.GetTask(id)
	if err != nil {
		return fmt.Errorf("task %s not found", id)
	}

	isEpicChild := task.EpicID != ""

	if *status != "" && isEpicChild {
		return fmt.Errorf("--status cannot be used with epic children")
	}

	// Build reorder options.
	opts := db.ReorderOpts{
		Position: *position,
		Top:      *top,
		Bottom:   *bottom,
		Before:   *before,
		After:    *after,
	}

	if isEpicChild {
		if err := d.ReorderInEpicOpts(id, task.EpicID, opts); err != nil {
			return err
		}
	} else {
		if err := d.ReorderOpt(id, *status, opts); err != nil {
			return err
		}
	}

	fmt.Printf("reordered %s\n", id)
	return nil
}
