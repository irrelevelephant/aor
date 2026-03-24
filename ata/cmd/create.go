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
	desc := fs.String("description", "", "Task description (markdown)")
	fs.StringVar(desc, "desc", "", "Task description (markdown)")
	status := fs.String("status", "", "Initial status (backlog|queue, default: inherit from epic or queue)")
	epicID := fs.String("epic", "", "Parent epic ID")
	tagStr := fs.String("tag", "", "Tags (comma-separated)")
	workspace := fs.String("workspace", "", "Workspace path (default: auto-detect)")
	jsonOut := fs.Bool("json", false, "Output JSON")

	// Separate flags from positional args since Go's flag stops at first non-flag.
	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"description": true, "desc": true, "status": true, "epic": true, "workspace": true, "tag": true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	// Collect the title from positional args.
	title := strings.TrimSpace(strings.Join(positional, " "))
	if title == "" {
		return exitUsage("usage: ata create TITLE [--description TEXT] [--status backlog|queue] [--epic ID] [--workspace PATH] [--json]")
	}

	if *status != "" && *status != model.StatusBacklog && *status != model.StatusQueue {
		return fmt.Errorf("status must be '%s' or '%s', got %q", model.StatusBacklog, model.StatusQueue, *status)
	}

	ws := *workspace
	if ws == "" {
		ws = detectWorkspace(d)
	} else if resolved, err := d.ResolveWorkspace(ws); err == nil {
		ws = resolved
	}

	createdIn := rawWorkingDir()
	task, err := d.CreateTask(title, *desc, *status, *epicID, ws, createdIn)
	if err != nil {
		return err
	}

	// Add tags if provided.
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
