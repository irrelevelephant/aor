package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
)

func Edit(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	title := fs.String("title", "", "New title")
	desc := fs.String("description", "", "New description (tasks only)")
	fs.StringVar(desc, "desc", "", "New description (tasks only)")
	descFile := fs.String("desc-file", "", "Read description from file (tasks only)")
	spec := fs.String("spec", "", "New spec (epics only)")
	specFile := fs.String("spec-file", "", "Read spec from file (epics only)")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"title": true, "description": true, "desc": true, "desc-file": true,
		"spec": true, "spec-file": true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata edit ID [--title TITLE] [--description TEXT] [--desc-file PATH] [--spec SPEC] [--spec-file PATH] [--json]")
	}

	id := positional[0]

	// Check mutual exclusivity.
	descFlagSet := flagWasSet(fs, "description") || flagWasSet(fs, "desc")
	descFileSet := flagWasSet(fs, "desc-file")
	if descFlagSet && descFileSet {
		return fmt.Errorf("--description and --desc-file are mutually exclusive")
	}
	if flagWasSet(fs, "spec") && flagWasSet(fs, "spec-file") {
		return fmt.Errorf("--spec and --spec-file are mutually exclusive")
	}

	hasDesc := descFlagSet || descFileSet
	hasSpec := flagWasSet(fs, "spec") || flagWasSet(fs, "spec-file")

	// Validate epic vs task field usage.
	if hasDesc || hasSpec {
		task, err := d.GetTask(id)
		if err != nil {
			return err
		}
		if task.IsEpic && hasDesc {
			return fmt.Errorf("use --spec/--spec-file for epics")
		}
		if !task.IsEpic && hasSpec {
			return fmt.Errorf("use --description/--desc-file for tasks")
		}
	}

	// Build update params: nil = don't change.
	var pTitle, pBody, pSpec *string

	if flagWasSet(fs, "title") {
		pTitle = title
	}
	if descFlagSet {
		pBody = desc
	}
	if descFileSet {
		s, err := readFileString(*descFile)
		if err != nil {
			return fmt.Errorf("read desc file: %w", err)
		}
		pBody = &s
	}
	if flagWasSet(fs, "spec") {
		pSpec = spec
	}
	if flagWasSet(fs, "spec-file") {
		s, err := readFileString(*specFile)
		if err != nil {
			return fmt.Errorf("read spec file: %w", err)
		}
		pSpec = &s
	}

	if pTitle == nil && pBody == nil && pSpec == nil {
		return exitUsage("at least one of --title, --description, --desc-file, --spec, or --spec-file is required")
	}

	task, err := d.UpdateTask(id, pTitle, pBody, pSpec)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(task)
	}
	fmt.Printf("updated %s\n", id)
	return nil
}
