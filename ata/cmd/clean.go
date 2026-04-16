package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"aor/ata/db"
)

func Clean(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	force := fs.Bool("force", false, "Skip confirmation prompt")
	closed := fs.Bool("closed", false, "Only delete closed tasks")
	olderThan := fs.String("older-than", "", "Only closed tasks older than N days (e.g. 30d); implies --closed")
	all := fs.Bool("all", false, "Delete ALL tasks (not just closed)")
	jsonOut := fs.Bool("json", false, "Output deleted task list as JSON")

	flagArgs, _ := splitFlagsAndPositional(args, map[string]bool{
		"older-than": true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	// --older-than implies --closed.
	if *olderThan != "" {
		*closed = true
	}

	if *all {
		return cleanAll(d, *force)
	}
	return cleanClosed(d, *olderThan, *force, *jsonOut)
}

// cleanClosed deletes only closed tasks (GC mode).
func cleanClosed(d *db.DB, olderThan string, force, jsonOut bool) error {
	var ageDur time.Duration
	if olderThan != "" {
		parsed, err := db.ParseDayDuration(olderThan)
		if err != nil {
			return err
		}
		ageDur = parsed
	}

	tasks, err := d.ListClosedTasks(ageDur)
	if err != nil {
		return err
	}

	if len(tasks) == 0 {
		fmt.Println("nothing to clean")
		return nil
	}

	// Get attachment summaries for all candidates.
	taskIDs := make([]string, len(tasks))
	for i, t := range tasks {
		taskIDs[i] = t.ID
	}
	attSummaries, err := d.GetAttachmentSummaries(taskIDs)
	if err != nil {
		return fmt.Errorf("get attachment info: %w", err)
	}

	// Compute totals.
	var totalAttachments int
	var totalAttachmentSize int64
	var epicCount int
	for _, t := range tasks {
		if t.IsEpic {
			epicCount++
		}
		if s, ok := attSummaries[t.ID]; ok {
			totalAttachments += s.Count
			totalAttachmentSize += s.TotalSize
		}
	}

	if !jsonOut {
		// Display summary table.
		fmt.Printf("%-8s  %-40s  %-10s  %-12s  %s\n", "ID", "TITLE", "TYPE", "CLOSED", "ATTACH")
		for _, t := range tasks {
			title := t.Title
			if len(title) > 40 {
				title = title[:37] + "..."
			}

			taskType := "task"
			if t.IsEpic {
				taskType = "epic"
			} else if t.EpicID != "" {
				taskType = "subtask"
			}

			closedDate := ""
			if t.ClosedAt != "" && len(t.ClosedAt) >= 10 {
				closedDate = t.ClosedAt[:10]
			}

			attCount := 0
			if s, ok := attSummaries[t.ID]; ok {
				attCount = s.Count
			}

			fmt.Printf("%-8s  %-40s  %-10s  %-12s  %d\n", t.ID, title, taskType, closedDate, attCount)
		}

		// Print totals.
		fmt.Printf("\nTotal: %d tasks", len(tasks))
		if epicCount > 0 {
			fmt.Printf(" (%d epics)", epicCount)
		}
		if totalAttachments > 0 {
			fmt.Printf(", %d attachments (%s)", totalAttachments, db.FormatBytes(totalAttachmentSize))
		}
		fmt.Println()
	}

	// Confirm.
	if !force {
		if !promptConfirm(fmt.Sprintf("Delete these %d closed tasks? [y/N] ", len(tasks)), "y") {
			fmt.Println("aborted")
			return nil
		}
	}

	// Delete from DB.
	deleted, err := d.GCClosedTasks(taskIDs)
	if err != nil {
		return err
	}

	removedDirs := removeAttachmentDirs(taskIDs)

	if jsonOut {
		return outputJSON(tasks)
	}

	fmt.Printf("deleted %d tasks", deleted)
	if removedDirs > 0 {
		fmt.Printf(", removed %d attachment directories", removedDirs)
	}
	fmt.Println()
	return nil
}

// cleanAll is the nuclear option: delete ALL tasks.
func cleanAll(d *db.DB, force bool) error {
	allTasks, err := d.ListTasks("", "", "", "")
	if err != nil {
		return err
	}
	taskIDs := make([]string, len(allTasks))
	for i, t := range allTasks {
		taskIDs[i] = t.ID
	}

	if !force {
		fmt.Printf("This will permanently delete ALL %d tasks.\n", len(allTasks))
		if !promptConfirm(`Type "yes" to confirm: `, "yes") {
			fmt.Println("aborted")
			return nil
		}
	}

	if _, err := d.Exec(`DELETE FROM tasks`); err != nil {
		return fmt.Errorf("delete tasks: %w", err)
	}

	removedDirs := removeAttachmentDirs(taskIDs)

	fmt.Printf("deleted %d tasks\n", len(allTasks))
	if removedDirs > 0 {
		fmt.Printf("removed %d attachment directories\n", removedDirs)
	}
	return nil
}

// removeAttachmentDirs removes attachment directories from disk for the given task IDs.
// Returns the number of directories successfully removed.
func removeAttachmentDirs(taskIDs []string) int {
	attDir, err := db.AttachmentsDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not determine attachments directory: %v\n", err)
		return 0
	}

	var removed int
	for _, id := range taskIDs {
		dir := filepath.Join(attDir, id)
		if _, err := os.Lstat(dir); err != nil {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to remove attachments for %s: %v\n", id, err)
		} else {
			removed++
		}
	}
	return removed
}
