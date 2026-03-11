package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
)

func Reorder(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("reorder", flag.ContinueOnError)
	position := fs.Int("position", -1, "Target position")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"position": true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 || *position < 0 {
		return exitUsage("usage: ata reorder ID --position N")
	}

	id := positional[0]
	if err := d.Reorder(id, *position, ""); err != nil {
		return err
	}

	fmt.Printf("reordered %s to position %d\n", id, *position)
	return nil
}
