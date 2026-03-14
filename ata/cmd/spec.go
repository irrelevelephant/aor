package cmd

import (
	"flag"
	"fmt"
	"os"

	"aor/ata/db"
)

func Spec(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("spec", flag.ContinueOnError)
	setFile := fs.String("set-file", "", "Set spec from file")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"set-file": true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata spec ID [--set-file PATH]")
	}

	id := positional[0]

	if *setFile != "" {
		data, err := os.ReadFile(*setFile)
		if err != nil {
			return fmt.Errorf("read spec file: %w", err)
		}
		task, err := d.UpdateSpec(id, string(data))
		if err != nil {
			return err
		}
		if *jsonOut {
			return outputJSON(task)
		}
		fmt.Printf("updated spec for %s\n", id)
		return nil
	}

	// Show spec.
	task, err := d.GetTask(id)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(map[string]string{"id": task.ID, "spec": task.Spec})
	}

	if task.Spec == "" {
		fmt.Printf("task %s has no spec\n", id)
	} else {
		fmt.Println(task.Spec)
	}
	return nil
}
