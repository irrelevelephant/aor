package cmd

import (
	"flag"
	"fmt"
	"strings"

	"aor/ata/db"
)

func Show(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata show ID [--json]")
	}

	id := positional[0]
	twc, err := d.GetTaskWithComments(id)
	if err != nil {
		return err
	}

	// Load tags.
	tags, _ := d.GetTags(id)

	// Load attachments.
	attachments, _ := d.ListAttachments(id)

	if *jsonOut {
		twc.Tags = tags
		twc.Attachments = attachments
		return outputJSON(twc)
	}

	t := twc.Task
	fmt.Printf("%s: %s\n", t.ID, t.Title)
	fmt.Printf("Status: %s\n", t.Status)
	if len(tags) > 0 {
		fmt.Printf("Tags: %s\n", strings.Join(tags, ", "))
	}
	if t.EpicID != "" {
		fmt.Printf("Epic: %s\n", t.EpicID)
	}
	if t.IsEpic {
		fmt.Println("Type: epic")
	}
	fmt.Printf("Workspace: %s\n", t.Workspace)

	// Show dependency info.
	blockers, _ := d.GetBlockers(id, true)
	if len(blockers) > 0 {
		fmt.Print("Blocked by:")
		for _, b := range blockers {
			fmt.Printf(" %s", b.ID)
		}
		fmt.Println()
	}
	blocking, _ := d.GetBlocking(id)
	if len(blocking) > 0 {
		fmt.Print("Blocks:")
		for _, b := range blocking {
			fmt.Printf(" %s", b.ID)
		}
		fmt.Println()
	}

	if t.IsEpic {
		if t.Spec != "" {
			fmt.Printf("\nSpec:\n%s\n", t.Spec)
		}
	} else {
		if t.Body != "" {
			fmt.Printf("\nDescription:\n%s\n", t.Body)
		}
	}
	if t.CloseReason != "" {
		fmt.Printf("\nClose reason: %s\n", t.CloseReason)
	}
	if len(twc.Comments) > 0 {
		fmt.Println("\n--- Comments ---")
		for _, c := range twc.Comments {
			fmt.Printf("[%s] %s: %s\n", c.CreatedAt, c.Author, c.Body)
		}
	}
	return nil
}
