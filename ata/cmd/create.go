package cmd

import (
	"flag"
	"fmt"
	"strings"

	"aor/ata/db"
	"aor/ata/model"
)

func Create(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	body := fs.String("body", "", "Task body (markdown)")
	bodyFile := fs.String("body-file", "", "Read body from file")
	status := fs.String("status", "", "Initial status (backlog|queue, default: inherit from epic or queue)")
	epicID := fs.String("epic", "", "Parent epic ID")
	tagStr := fs.String("tag", "", "Tags (comma-separated)")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"body": true, "body-file": true, "status": true, "epic": true, "tag": true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	title := strings.TrimSpace(strings.Join(positional, " "))
	if title == "" {
		return exitUsage("usage: ata create TITLE [--body TEXT] [--body-file PATH] [--status backlog|queue] [--epic ID] [--json]")
	}

	bodyText, _, err := resolveBody(fs, body, bodyFile, true)
	if err != nil {
		return err
	}

	if *status != "" && *status != model.StatusBacklog && *status != model.StatusQueue {
		return fmt.Errorf("status must be '%s' or '%s', got %q", model.StatusBacklog, model.StatusQueue, *status)
	}

	createdIn := rawWorkingDir()
	task, err := d.CreateTask(title, bodyText, *status, *epicID, createdIn)
	if err != nil {
		return err
	}

	for _, t := range db.SplitComma(*tagStr) {
		d.AddTag(task.ID, t)
	}

	if *jsonOut {
		tags, _ := d.GetTags(task.ID)
		task.Tags = tags
		return outputJSON(task)
	}

	var tagSuffix string
	if tags, _ := d.GetTags(task.ID); len(tags) > 0 {
		tagSuffix = " [" + strings.Join(tags, ", ") + "]"
	}
	fmt.Printf("created %s: %s [%s]%s\n", task.ID, task.Title, task.Status, tagSuffix)
	return nil
}
