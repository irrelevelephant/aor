package cmd

import (
	"flag"
	"fmt"
	"strings"

	"aor/ata/db"
)

func Create(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	body := fs.String("body", "", "Task body/description (markdown)")
	status := fs.String("status", "queue", "Initial status (backlog|queue)")
	epicID := fs.String("epic", "", "Parent epic ID")
	tagStr := fs.String("tag", "", "Tags (comma-separated)")
	workspace := fs.String("workspace", "", "Workspace path (default: auto-detect)")
	jsonOut := fs.Bool("json", false, "Output JSON")

	// Separate flags from positional args since Go's flag stops at first non-flag.
	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"body": true, "status": true, "epic": true, "workspace": true, "tag": true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	// Collect the title from positional args.
	title := strings.TrimSpace(strings.Join(positional, " "))
	if title == "" {
		return exitUsage("usage: ata create TITLE [--status backlog|queue] [--epic ID] [--workspace PATH] [--json]")
	}

	if *status != "backlog" && *status != "queue" {
		return fmt.Errorf("status must be 'backlog' or 'queue', got %q", *status)
	}

	ws := *workspace
	if ws == "" {
		ws = detectWorkspace(d)
	}

	createdIn := rawWorkingDir()
	task, err := d.CreateTask(title, *body, *status, *epicID, ws, createdIn)
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
