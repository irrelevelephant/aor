package cmd

import (
	"flag"
	"fmt"
	"strconv"
	"strings"

	"aor/ata/db"
)

func Comment(d *db.DB, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "edit":
			return commentEdit(d, args[1:])
		case "rm", "remove", "delete":
			return commentDelete(d, args[1:])
		}
	}
	return commentAdd(d, args)
}

func commentAdd(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("comment", flag.ContinueOnError)
	author := fs.String("author", "human", "Comment author (human|agent|system)")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"author": true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 2 {
		return exitUsage(`usage: ata comment ID BODY [--author human|agent|system]
       ata comment edit COMMENT_ID BODY
       ata comment rm COMMENT_ID`)
	}

	id := positional[0]
	body := strings.Join(positional[1:], " ")

	comment, err := d.AddComment(id, body, *author)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(comment)
	}

	fmt.Printf("added comment to %s\n", id)
	return nil
}

func commentEdit(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("comment edit", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 2 {
		return exitUsage("usage: ata comment edit COMMENT_ID BODY")
	}

	cid, err := strconv.Atoi(positional[0])
	if err != nil {
		return fmt.Errorf("invalid comment id %q", positional[0])
	}
	body := strings.Join(positional[1:], " ")

	comment, err := d.UpdateComment(cid, body)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(comment)
	}

	fmt.Printf("edited comment %d on %s\n", comment.ID, comment.TaskID)
	return nil
}

func commentDelete(d *db.DB, args []string) error {
	if len(args) < 1 {
		return exitUsage("usage: ata comment rm COMMENT_ID")
	}

	cid, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid comment id %q", args[0])
	}

	taskID, err := d.DeleteComment(cid)
	if err != nil {
		return err
	}

	fmt.Printf("deleted comment %d from %s\n", cid, taskID)
	return nil
}
