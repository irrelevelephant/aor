package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
	"aor/ata/model"
)

func EpicCloseEligible(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("epic-close-eligible", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Filter by workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	epics, err := d.EpicCloseEligible(*workspace)
	if err != nil {
		return err
	}

	// Auto-close them.
	var closed []model.Task
	for _, e := range epics {
		t, err := d.CloseTask(e.ID, "all children closed")
		if err == nil {
			closed = append(closed, *t)
		}
	}

	if *jsonOut {
		result := struct {
			Closed []string `json:"closed"`
			Count  int      `json:"count"`
		}{
			Count: len(closed),
		}
		for _, t := range closed {
			result.Closed = append(result.Closed, t.ID)
		}
		if result.Closed == nil {
			result.Closed = []string{}
		}
		return outputJSON(result)
	}

	if len(closed) == 0 {
		fmt.Println("no epics eligible for close")
		return nil
	}

	for _, t := range closed {
		fmt.Printf("closed epic %s: %s\n", t.ID, t.Title)
	}
	return nil
}
