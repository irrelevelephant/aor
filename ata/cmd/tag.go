package cmd

import (
	"flag"
	"fmt"
	"strings"

	"aor/ata/db"
)

// IsTagBulkSubcommand reports whether arg is a tag subcommand that accepts
// piped task IDs (add/rm/remove).
func IsTagBulkSubcommand(arg string) bool {
	switch arg {
	case "add", "rm", "remove":
		return true
	}
	return false
}

func Tag(d *db.DB, args []string) error {
	if len(args) == 0 {
		return exitUsage(`usage: ata tag <subcommand> [args]

Subcommands:
  add  TASK TAG [TAG...]  Add tags to a task (TASK can come from stdin)
  rm   TASK TAG [TAG...]  Remove tags from a task (TASK can come from stdin)
  list [--json]           List all tags in use`)
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "add":
		return tagAdd(d, rest)
	case "rm", "remove":
		return tagRemove(d, rest)
	case "list", "ls":
		return tagList(d, rest)
	default:
		return fmt.Errorf("unknown tag subcommand: %s", sub)
	}
}

// tagResolveIDsAndArgs returns (taskIDs, tagArgs). When stdin/--ids supplies
// IDs, all positional args are tag names; otherwise positional[0] is the task
// ID and positional[1:] are the tag names (legacy form).
func tagResolveIDsAndArgs(args []string, usage string) ([]string, []string, error) {
	fs := flag.NewFlagSet("tag", flag.ContinueOnError)
	idsFlag := fs.String("ids", "", "Whitespace-separated task IDs (use instead of/with stdin)")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{"ids": true})
	if err := fs.Parse(flagArgs); err != nil {
		return nil, nil, err
	}

	stdinIDs, err := resolveIDsFlag(fs, idsFlag)
	if err != nil {
		return nil, nil, err
	}

	if len(stdinIDs) > 0 {
		if len(positional) == 0 {
			return nil, nil, exitUsage(usage)
		}
		return stdinIDs, positional, nil
	}
	if len(positional) < 2 {
		return nil, nil, exitUsage(usage)
	}
	return positional[:1], positional[1:], nil
}

func tagAdd(d *db.DB, args []string) error {
	taskIDs, tagNames, err := tagResolveIDsAndArgs(args,
		"usage: ata tag add TASK TAG [TAG...]\n       <ID list> | ata tag add TAG [TAG...]")
	if err != nil {
		return err
	}

	for _, taskID := range taskIDs {
		task, err := d.GetTask(taskID)
		if err != nil {
			return err
		}

		for _, tag := range tagNames {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			if err := d.AddTag(taskID, tag); err != nil {
				return err
			}
		}

		tags, _ := d.GetTags(taskID)
		fmt.Printf("%s (%s): tags [%s]\n", task.ID, task.Title, strings.Join(tags, ", "))
	}
	return nil
}

func tagRemove(d *db.DB, args []string) error {
	taskIDs, tagNames, err := tagResolveIDsAndArgs(args,
		"usage: ata tag rm TASK TAG [TAG...]\n       <ID list> | ata tag rm TAG [TAG...]")
	if err != nil {
		return err
	}

	for _, taskID := range taskIDs {
		if _, err := d.GetTask(taskID); err != nil {
			return err
		}
		for _, tag := range tagNames {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			if err := d.RemoveTag(taskID, tag); err != nil {
				return err
			}
			fmt.Printf("removed tag %q from %s\n", tag, taskID)
		}
	}
	return nil
}

func tagList(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("tag list", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	tags, err := d.ListAllTags()
	if err != nil {
		return err
	}

	if *jsonOut {
		if tags == nil {
			tags = []string{}
		}
		return outputJSON(tags)
	}

	if len(tags) == 0 {
		fmt.Println("no tags in use")
		return nil
	}

	for _, t := range tags {
		fmt.Println(t)
	}
	return nil
}
