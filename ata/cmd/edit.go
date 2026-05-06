package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
	"aor/ata/model"
)

func Edit(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	title := fs.String("title", "", "New title")
	body := fs.String("body", "", "New body")
	bodyFile := fs.String("body-file", "", "Read body from file")
	epic := fs.String("epic", "", "Parent epic ID (use 'none' to remove from epic)")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"title": true, "body": true, "body-file": true, "epic": true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata edit ID [--title TITLE] [--body TEXT] [--body-file PATH] [--epic EPIC_ID|none] [--json]")
	}

	id := positional[0]

	bodyText, bodySet, err := resolveBody(fs, body, bodyFile, true)
	if err != nil {
		return err
	}

	epicSet := flagWasSet(fs, "epic")

	var pTitle, pBody *string
	if flagWasSet(fs, "title") {
		pTitle = title
	}
	if bodySet {
		pBody = &bodyText
	}

	if pTitle == nil && pBody == nil && !epicSet {
		return exitUsage("at least one of --title, --body, --body-file, or --epic is required")
	}

	var task *model.Task
	if epicSet {
		t, err := d.GetTask(id)
		if err != nil {
			return err
		}
		task = t
		if t.IsEpic {
			return fmt.Errorf("cannot reparent an epic; only tasks can have a parent epic")
		}
	}

	if pTitle != nil || pBody != nil {
		t, err := d.UpdateTask(id, pTitle, pBody)
		if err != nil {
			return err
		}
		task = t
	}

	if epicSet {
		newEpicID := *epic
		if newEpicID == "none" {
			newEpicID = ""
		}
		if err := d.SetEpicID(id, newEpicID); err != nil {
			return err
		}
	}

	if *jsonOut {
		return outputJSON(task)
	}
	fmt.Printf("updated %s\n", id)
	return nil
}
