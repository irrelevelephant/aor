package cmd

import (
	"flag"
	"fmt"

	"aor/ata/db"
)

func EpicCloseEligible(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("epic-close-eligible", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Filter by workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")
	doClose := fs.Bool("close", false, "Actually close eligible epics (default: list only)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	epics, err := d.EpicCloseEligible(*workspace)
	if err != nil {
		return err
	}

	if !*doClose {
		// List-only mode: return eligible epics without closing.
		if *jsonOut {
			return outputJSON(epics)
		}
		if len(epics) == 0 {
			fmt.Println("no epics eligible for close")
			return nil
		}
		for _, e := range epics {
			fmt.Printf("eligible: %s — %s\n", e.ID, e.Title)
		}
		return nil
	}

	// Close mode: close eligible epics and report.
	var closedIDs []string
	for _, e := range epics {
		if _, err := d.CloseTask(e.ID, "all children closed"); err == nil {
			closedIDs = append(closedIDs, e.ID)
		}
	}

	if *jsonOut {
		result := struct {
			Closed []string `json:"closed"`
			Count  int      `json:"count"`
		}{
			Closed: closedIDs,
			Count:  len(closedIDs),
		}
		if result.Closed == nil {
			result.Closed = []string{}
		}
		return outputJSON(result)
	}

	if len(closedIDs) == 0 {
		fmt.Println("no epics eligible for close")
		return nil
	}

	for _, id := range closedIDs {
		fmt.Printf("closed epic %s\n", id)
	}
	return nil
}
