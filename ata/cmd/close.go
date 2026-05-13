package cmd

import (
	"flag"
	"strings"

	"aor/ata/db"
	"aor/ata/model"
)

func Close(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("close", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")
	idsFlag := fs.String("ids", "", "Whitespace-separated task IDs")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{"ids": true})

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	stdinIDs, err := resolveIDsFlag(fs, idsFlag)
	if err != nil {
		return err
	}

	var ids []string
	var reason string
	if len(stdinIDs) > 0 {
		ids = stdinIDs
		reason = strings.Join(positional, " ")
	} else {
		if len(positional) == 0 {
			return exitUsage("usage: ata close ID [REASON]\n       <ID list> | ata close [REASON]")
		}
		ids = positional[:1]
		reason = strings.Join(positional[1:], " ")
	}

	closed, err := collectTasks(ids, func(id string) (*model.Task, error) {
		return d.CloseTask(id, reason)
	})
	if err != nil {
		return err
	}
	return emitTasks("closed", closed, *jsonOut)
}
