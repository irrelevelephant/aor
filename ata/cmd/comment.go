package cmd

import (
	"flag"
	"fmt"
	"strings"

	"aor/ata/db"
)

func Comment(d *db.DB, args []string) error {
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
		return exitUsage("usage: ata comment ID BODY [--author human|agent|system]")
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
