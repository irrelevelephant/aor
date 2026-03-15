package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
)

func Edit(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	title := fs.String("title", "", "New title")
	body := fs.String("body", "", "New body")
	bodyFile := fs.String("body-file", "", "Read body from file")
	spec := fs.String("spec", "", "New spec")
	specFile := fs.String("spec-file", "", "Read spec from file")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"title": true, "body": true, "body-file": true,
		"spec": true, "spec-file": true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata edit ID [--title TITLE] [--body BODY] [--body-file PATH] [--spec SPEC] [--spec-file PATH] [--json]")
	}

	id := positional[0]

	// Check mutual exclusivity.
	if flagWasSet(fs, "body") && flagWasSet(fs, "body-file") {
		return fmt.Errorf("--body and --body-file are mutually exclusive")
	}
	if flagWasSet(fs, "spec") && flagWasSet(fs, "spec-file") {
		return fmt.Errorf("--spec and --spec-file are mutually exclusive")
	}

	// Build update params: nil = don't change.
	var pTitle, pBody, pSpec *string

	if flagWasSet(fs, "title") {
		pTitle = title
	}
	if flagWasSet(fs, "body") {
		pBody = body
	}
	if flagWasSet(fs, "body-file") {
		s, err := readFileString(*bodyFile)
		if err != nil {
			return fmt.Errorf("read body file: %w", err)
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
		return exitUsage("at least one of --title, --body, --body-file, --spec, or --spec-file is required")
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
