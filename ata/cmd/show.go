package cmd

import (
	"flag"
	"fmt"
	"slices"
	"strings"

	"aor/ata/db"
	"aor/ata/model"
)

func Show(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	stdinIDs, err := readIDsFromStdin()
	if err != nil {
		return err
	}
	ids := append(slices.Clone(positional), stdinIDs...)

	if len(ids) == 0 {
		return exitUsage("usage: ata show ID [ID...] [--json]\n       <ID list> | ata show [--json]")
	}

	tagMap, _ := d.GetTagsForTasks(ids)

	twcs := make([]*model.TaskWithComments, 0, len(ids))
	for _, id := range ids {
		twc, err := d.GetTaskWithComments(id)
		if err != nil {
			return err
		}
		attachments, _ := d.ListAttachments(id)
		twc.Tags = tagMap[id]
		twc.Attachments = attachments
		twcs = append(twcs, twc)
	}

	if *jsonOut {
		if len(twcs) == 1 {
			return outputJSON(twcs[0])
		}
		return outputJSON(twcs)
	}

	for i, twc := range twcs {
		if i > 0 {
			fmt.Println("\n---")
		}
		printTaskText(d, twc)
	}
	return nil
}

func printTaskText(d *db.DB, twc *model.TaskWithComments) {
	t := twc.Task
	fmt.Printf("%s: %s\n", t.ID, t.Title)
	fmt.Printf("Status: %s\n", t.Status)
	if len(twc.Tags) > 0 {
		fmt.Printf("Tags: %s\n", strings.Join(twc.Tags, ", "))
	}
	if t.EpicID != "" {
		fmt.Printf("Epic: %s\n", t.EpicID)
	}
	if t.IsEpic {
		fmt.Println("Type: epic")
	}

	blockers, _ := d.GetBlockers(t.ID, true)
	if len(blockers) > 0 {
		fmt.Print("Blocked by:")
		for _, b := range blockers {
			fmt.Printf(" %s", b.ID)
		}
		fmt.Println()
	}
	blocking, _ := d.GetBlocking(t.ID)
	if len(blocking) > 0 {
		fmt.Print("Blocks:")
		for _, b := range blocking {
			fmt.Printf(" %s", b.ID)
		}
		fmt.Println()
	}

	if t.Body != "" {
		fmt.Printf("\nDescription:\n%s\n", t.Body)
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
}
