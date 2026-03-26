package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
)

func Spec(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("spec", flag.ContinueOnError)
	setFlag := fs.String("set", "", "Set spec from text")
	setFile := fs.String("set-file", "", "Set spec from file")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"set": true, "set-file": true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata spec ID [--set TEXT] [--set-file PATH]")
	}

	if flagWasSet(fs, "set") && flagWasSet(fs, "set-file") {
		return fmt.Errorf("--set and --set-file are mutually exclusive")
	}

	id := positional[0]

	// Validate that the task is an epic.
	task, err := d.GetTask(id)
	if err != nil {
		return err
	}
	if !task.IsEpic {
		return fmt.Errorf("spec is only for epics; use 'ata edit %s --description' for tasks", id)
	}

	if flagWasSet(fs, "set") || flagWasSet(fs, "set-file") {
		s := *setFlag
		if flagWasSet(fs, "set-file") {
			var err error
			s, err = readFileString(*setFile)
			if err != nil {
				return fmt.Errorf("read spec file: %w", err)
			}
		}
		task, err := d.UpdateTask(id, nil, nil, &s)
		if err != nil {
			return err
		}
		if *jsonOut {
			return outputJSON(task)
		}
		fmt.Printf("updated spec for %s\n", id)
		return nil
	}

	if *jsonOut {
		return outputJSON(map[string]string{"id": task.ID, "spec": task.Spec})
	}

	if task.Spec == "" {
		fmt.Printf("epic %s has no spec\n", id)
	} else {
		fmt.Println(task.Spec)
	}
	return nil
}
