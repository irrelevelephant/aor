package cmd

import (
	"flag"
	"fmt"

	"aor/afl/db"
	"aor/afl/model"
)

// PathCmd routes path subcommands.
func PathCmd(d *db.DB, args []string) error {
	if len(args) == 0 {
		return pathUsage()
	}

	switch args[0] {
	case "create":
		return pathCreate(d, args[1:])
	case "list", "ls":
		return pathList(d, args[1:])
	case "delete", "rm":
		return pathDelete(d, args[1:])
	default:
		return pathUsage()
	}
}

func pathCreate(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("path create", flag.ContinueOnError)
	pathType := fs.String("type", "", "Path type: happy, alternate, error")
	name := fs.String("name", "", "Path name")
	workspace := fs.String("workspace", "", "Workspace")
	order := fs.Int("order", 0, "Sort order")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"type": true, "name": true, "workspace": true, "order": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("usage: afl path create <FLOW-ID> --type <happy|alternate|error> --name <name> [--workspace <ws>] [--json]")
	}
	if *pathType == "" {
		return fmt.Errorf("--type is required (happy, alternate, error)")
	}
	if !model.IsValidPathType(*pathType) {
		return fmt.Errorf("invalid path type %q: must be happy, alternate, or error", *pathType)
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}

	flowID := positional[0]
	ws := resolveOrDetectWorkspace(d, *workspace)

	flow, err := d.ResolveFlow(ws, flowID)
	if err != nil {
		return err
	}
	if flow == nil {
		return fmt.Errorf("flow %q not found in workspace %s", flowID, ws)
	}

	p, err := d.CreatePath(flow.ID, *pathType, *name, *order)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(p)
	}

	fmt.Printf("created path: %s %q (%s)\n", p.PathType, p.Name, p.ID)
	return nil
}

func pathList(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("path list", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"workspace": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("usage: afl path list <FLOW-ID> [--workspace <ws>] [--json]")
	}

	flowID := positional[0]
	ws := resolveOrDetectWorkspace(d, *workspace)

	flow, err := d.ResolveFlow(ws, flowID)
	if err != nil {
		return err
	}
	if flow == nil {
		return fmt.Errorf("flow %q not found in workspace %s", flowID, ws)
	}

	paths, err := d.ListPaths(flow.ID)
	if err != nil {
		return err
	}

	if *jsonOut {
		if paths == nil {
			paths = []model.Path{}
		}
		return outputJSON(paths)
	}

	if len(paths) == 0 {
		fmt.Println("no paths")
		return nil
	}

	for _, p := range paths {
		fmt.Printf("%s  [%s]  %s\n", p.ID, p.PathType, p.Name)
	}
	return nil
}

func pathDelete(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("path delete", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("usage: afl path delete <path-id> [--json]")
	}

	pathID := positional[0]

	if err := d.DeletePath(pathID); err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(map[string]any{"deleted": pathID})
	}

	fmt.Printf("deleted path: %s\n", pathID)
	return nil
}

func pathUsage() error {
	return fmt.Errorf(`usage: afl path <subcommand>

Subcommands:
  create <FLOW-ID>   Create a path
  list <FLOW-ID>     List paths
  delete <path-id>   Delete a path

Flags:
  --type <type>      Path type: happy, alternate, error (for create)
  --name <name>      Path name (for create)
  --order <n>        Sort order (for create)
  --workspace <ws>   Override workspace
  --json             Output JSON`)
}
