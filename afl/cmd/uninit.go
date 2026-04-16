package cmd

import (
	"flag"
	"fmt"

	"aor/afl/db"
)

// Uninit unregisters a workspace.
func Uninit(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("uninit", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	var nameOrPath string
	if len(positional) > 0 {
		nameOrPath = positional[0]
	}

	var ws string
	if nameOrPath == "" {
		ws = detectWorkspace(d)
	} else {
		resolved, err := d.ResolveWorkspace(nameOrPath)
		if err != nil {
			return err
		}
		ws = resolved
	}

	wsInfo, err := d.GetWorkspace(ws)
	if err != nil {
		return err
	}
	if wsInfo == nil {
		return fmt.Errorf("workspace %q is not registered", ws)
	}

	if err := d.UnregisterWorkspace(ws); err != nil {
		return err
	}

	displayName := ws
	if wsInfo.Name != "" {
		displayName = fmt.Sprintf("%s (%s)", wsInfo.Name, ws)
	}

	if *jsonOut {
		return outputJSON(map[string]any{
			"unregistered": ws,
			"name":         wsInfo.Name,
		})
	}

	fmt.Printf("unregistered workspace: %s\n", displayName)
	return nil
}
