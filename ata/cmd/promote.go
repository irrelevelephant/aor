package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
)

func Promote(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("promote", flag.ContinueOnError)
	specFlag := fs.String("spec", "", "Spec text (markdown)")
	specFile := fs.String("spec-file", "", "Path to spec file (markdown)")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"spec": true, "spec-file": true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata promote ID [--spec TEXT] [--spec-file PATH]")
	}

	if flagWasSet(fs, "spec") && flagWasSet(fs, "spec-file") {
		return fmt.Errorf("--spec and --spec-file are mutually exclusive")
	}

	id := positional[0]

	spec := ""
	if flagWasSet(fs, "spec") {
		spec = *specFlag
	} else if *specFile != "" {
		var err error
		spec, err = readFileString(*specFile)
		if err != nil {
			return fmt.Errorf("read spec file: %w", err)
		}
	}

	task, err := d.PromoteToEpic(id, spec)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(task)
	}

	fmt.Printf("promoted %s to epic: %s\n", task.ID, task.Title)
	return nil
}
