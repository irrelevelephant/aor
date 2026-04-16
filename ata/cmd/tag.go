package cmd

import (
	"flag"
	"fmt"
	"strings"

	"aor/ata/db"
)

func Tag(d *db.DB, args []string) error {
	if len(args) == 0 {
		return exitUsage(`usage: ata tag <subcommand> [args]

Subcommands:
  add  TASK TAG [TAG...]  Add tags to a task
  rm   TASK TAG [TAG...]  Remove tags from a task
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

func tagAdd(d *db.DB, args []string) error {
	if len(args) < 2 {
		return exitUsage("usage: ata tag add TASK TAG [TAG...]")
	}

	taskID := args[0]
	task, err := d.GetTask(taskID)
	if err != nil {
		return err
	}

	for _, tag := range args[1:] {
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
	return nil
}

func tagRemove(d *db.DB, args []string) error {
	if len(args) < 2 {
		return exitUsage("usage: ata tag rm TASK TAG [TAG...]")
	}

	taskID := args[0]
	if _, err := d.GetTask(taskID); err != nil {
		return err
	}

	for _, tag := range args[1:] {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if err := d.RemoveTag(taskID, tag); err != nil {
			return err
		}
		fmt.Printf("removed tag %q from %s\n", tag, taskID)
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
