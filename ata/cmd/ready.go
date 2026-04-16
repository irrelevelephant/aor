package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
	"aor/ata/model"
)

func Ready(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("ready", flag.ContinueOnError)
	epicID := fs.String("epic", "", "Filter by epic ID")
	tag := fs.String("tag", "", "Filter by tag")
	limit := fs.Int("limit", 0, "Max results (0 = unlimited)")
	jsonOut := fs.Bool("json", false, "Output JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	tasks, err := d.ReadyTasks(*epicID, *tag, *limit)
	if err != nil {
		return err
	}

	if *jsonOut {
		if tasks == nil {
			tasks = []model.Task{}
		}
		return outputJSON(tasks)
	}

	if len(tasks) == 0 {
		fmt.Println("no ready tasks")
		return nil
	}

	for _, t := range tasks {
		epic := ""
		if t.EpicID != "" {
			epic = fmt.Sprintf(" [epic:%s]", t.EpicID)
		}
		fmt.Printf("%-4s %s%s\n", t.ID, t.Title, epic)
	}
	return nil
}
