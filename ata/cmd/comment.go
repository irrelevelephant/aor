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
		}
		if IsCommentDeleteSubcommand(args[0]) {
			return commentDelete(d, args[1:])
		}
	}
	return commentAdd(d, args)
}

// IsCommentDeleteSubcommand reports whether arg is one of the comment-delete
// aliases (rm, remove, delete). Used by the remote-proxy code to skip stdin
// injection on delete.
func IsCommentDeleteSubcommand(arg string) bool {
	switch arg {
	case "rm", "remove", "delete":
		return true
	}
	return false
}

func commentAdd(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("comment", flag.ContinueOnError)
	author := fs.String("author", "human", "Comment author (human|agent|system)")
	body := fs.String("body", "", "Comment body (markdown)")
	bodyFile := fs.String("body-file", "", "Read body from file")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"author": true, "body": true, "body-file": true,
	})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return exitUsage(`usage: ata comment ID [BODY | --body TEXT | --body-file PATH | <stdin] [--author human|agent|system]
       ata comment edit COMMENT_ID [BODY | --body TEXT | --body-file PATH | <stdin]
       ata comment rm COMMENT_ID`)
	}

	id := positional[0]
	commentBody, err := resolveCommentBody(fs, body, bodyFile, positional[1:])
	if err != nil {
		return err
	}

	comment, err := d.AddComment(id, commentBody, *author)
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
	body := fs.String("body", "", "Comment body (markdown)")
	bodyFile := fs.String("body-file", "", "Read body from file")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"body": true, "body-file": true,
	})
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return exitUsage("usage: ata comment edit COMMENT_ID [BODY | --body TEXT | --body-file PATH | <stdin]")
	}

	cid, err := strconv.Atoi(positional[0])
	if err != nil {
		return fmt.Errorf("invalid comment id %q", positional[0])
	}

	commentBody, err := resolveCommentBody(fs, body, bodyFile, positional[1:])
	if err != nil {
		return err
	}

	comment, err := d.UpdateComment(cid, commentBody)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(comment)
	}

	fmt.Printf("edited comment %d on %s\n", comment.ID, comment.TaskID)
	return nil
}

// resolveCommentBody picks the comment body from --body / --body-file / stdin
// (via resolveBody) when set, otherwise from the trailing positional args.
func resolveCommentBody(fs *flag.FlagSet, body, bodyFile *string, positional []string) (string, error) {
	bodyText, bodySet, err := resolveBody(fs, body, bodyFile, true)
	if err != nil {
		return "", err
	}
	if bodySet {
		return bodyText, nil
	}
	if len(positional) > 0 {
		return strings.Join(positional, " "), nil
	}
	return "", exitUsage("comment body is required (positional, --body, --body-file, or piped stdin)")
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
