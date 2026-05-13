package cmd

import (
	"flag"
	"slices"

	"aor/ata/db"
	"aor/ata/model"
)

func Reopen(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("reopen", flag.ContinueOnError)
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
		return exitUsage("usage: ata reopen ID [ID...]\n       <ID list> | ata reopen")
	}

	reopened, err := collectTasks(ids, func(id string) (*model.Task, error) {
		return d.ReopenTask(id)
	})
	if err != nil {
		return err
	}
	return emitTasks("reopened", reopened, *jsonOut)
}
