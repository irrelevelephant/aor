package cmd

import (
	"flag"
	"fmt"
	"strings"

	"aor/ata/db"
	"aor/ata/model"
)

func List(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	status := fs.String("status", "", "Filter by status")
	epicID := fs.String("epic", "", "Filter by epic ID")
	tag := fs.String("tag", "", "Filter by tag")
	all := fs.Bool("all", false, "Include closed tasks")
	jsonOut := fs.Bool("json", false, "Output JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	tasks, err := d.ListTasks(*status, *epicID, *tag, "")
	if err != nil {
		return err
	}

	// Hide closed tasks unless --all or --status is explicitly set.
	if !*all && *status == "" {
		filtered := tasks[:0]
		for _, t := range tasks {
			if t.Status != model.StatusClosed {
				filtered = append(filtered, t)
			}
		}
		tasks = filtered
	}

	if *jsonOut {
		if tasks == nil {
			tasks = []model.Task{}
		}
		return outputJSON(tasks)
	}

	if len(tasks) == 0 {
		fmt.Println("no tasks found")
		return nil
	}

	// Batch-load tags for display.
	taskIDs := make([]string, len(tasks))
	for i, t := range tasks {
		taskIDs[i] = t.ID
	}
	tagMap, _ := d.GetTagsForTasks(taskIDs)

	for _, t := range tasks {
		epic := ""
		if t.EpicID != "" {
			epic = fmt.Sprintf(" [epic:%s]", t.EpicID)
		}
		isEpic := ""
		if t.IsEpic {
			isEpic = " (epic)"
		}
		tags := ""
		if tt := tagMap[t.ID]; len(tt) > 0 {
			tags = " [" + strings.Join(tt, ", ") + "]"
		}
		fmt.Printf("%-4s %-12s %s%s%s%s\n", t.ID, t.Status, t.Title, epic, isEpic, tags)
	}
	return nil
}
