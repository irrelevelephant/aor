package cmd

import (
	"flag"
	"fmt"

	"aor/afl/db"
	"aor/afl/model"
)

// StepCmd routes step subcommands.
func StepCmd(d *db.DB, args []string) error {
	if len(args) == 0 {
		return stepUsage()
	}

	switch args[0] {
	case "create":
		return stepCreate(d, args[1:])
	case "list", "ls":
		return stepList(d, args[1:])
	case "edit":
		return stepEdit(d, args[1:])
	case "delete", "rm":
		return stepDelete(d, args[1:])
	default:
		return stepUsage()
	}
}

func stepCreate(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("step create", flag.ContinueOnError)
	name := fs.String("name", "", "Step name")
	description := fs.String("description", "", "Step description")
	order := fs.Int("order", 0, "Sort order")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"name": true, "description": true, "order": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("usage: afl step create <path-id> --name <name> [--description <desc>] [--order <n>] [--json]")
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}

	pathID := positional[0]

	// Verify path exists.
	if _, err := d.GetPath(pathID); err != nil {
		return err
	}

	// Auto-assign order if not specified.
	sortOrder := *order
	if !flagWasSet(fs, "order") {
		steps, err := d.ListSteps(pathID)
		if err != nil {
			return err
		}
		if len(steps) > 0 {
			sortOrder = steps[len(steps)-1].SortOrder + 1
		}
	}

	step, err := d.CreateStep(pathID, *name, *description, sortOrder)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(step)
	}

	fmt.Printf("created step: %q (order %d, %s)\n", step.Name, step.SortOrder, step.ID)
	return nil
}

func stepList(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("step list", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("usage: afl step list <path-id> [--json]")
	}

	pathID := positional[0]

	steps, err := d.ListSteps(pathID)
	if err != nil {
		return err
	}

	if *jsonOut {
		if steps == nil {
			steps = []model.Step{}
		}
		return outputJSON(steps)
	}

	if len(steps) == 0 {
		fmt.Println("no steps")
		return nil
	}

	for _, s := range steps {
		desc := ""
		if s.Description != "" {
			desc = "  " + s.Description
		}
		fmt.Printf("%s  [%d] %s%s\n", s.ID, s.SortOrder, s.Name, desc)
	}
	return nil
}

func stepEdit(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("step edit", flag.ContinueOnError)
	name := fs.String("name", "", "New name")
	description := fs.String("description", "", "New description")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"name": true, "description": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("usage: afl step edit <step-id> [--name <name>] [--description <desc>] [--json]")
	}

	stepID := positional[0]

	var namePtr, descPtr *string
	if flagWasSet(fs, "name") {
		namePtr = name
	}
	if flagWasSet(fs, "description") {
		descPtr = description
	}

	if namePtr == nil && descPtr == nil {
		return fmt.Errorf("at least one of --name or --description is required")
	}

	step, err := d.UpdateStep(stepID, namePtr, descPtr)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(step)
	}

	fmt.Printf("updated step: %q (%s)\n", step.Name, step.ID)
	return nil
}

func stepDelete(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("step delete", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("usage: afl step delete <step-id> [--json]")
	}

	stepID := positional[0]

	if err := d.DeleteStep(stepID); err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(map[string]any{"deleted": stepID})
	}

	fmt.Printf("deleted step: %s\n", stepID)
	return nil
}

func stepUsage() error {
	return fmt.Errorf(`usage: afl step <subcommand>

Subcommands:
  create <path-id>   Create a step
  list <path-id>     List steps
  edit <step-id>     Edit a step
  delete <step-id>   Delete a step

Flags:
  --name <name>          Step name (for create/edit)
  --description <desc>   Step description (for create/edit)
  --order <n>            Sort order (for create)
  --json                 Output JSON`)
}
